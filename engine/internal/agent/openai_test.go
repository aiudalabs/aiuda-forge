package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer returns an httptest server that streams the given content deltas as
// OpenAI-style SSE chunks, recording the request path and decoded body.
func sseServer(t *testing.T, deltas []string, gotPath *string, gotBody *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, d := range deltas {
			chunk := fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, d)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestOpenAIBackendStreams(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := sseServer(t, []string{"Hello", ", ", "world"}, &gotPath, &gotBody)
	defer srv.Close()

	b := OpenAIBackend{BaseURL: srv.URL + "/v1", Model: "gpt-test"}
	var texts []string
	var sawResult bool
	res, err := b.Run(context.Background(), "hi", Options{}, func(e Event) {
		switch e.Kind {
		case KindText:
			texts = append(texts, e.Text)
		case KindResult:
			sawResult = true
			if e.Text != "Hello, world" {
				t.Fatalf("result event text = %q, want %q", e.Text, "Hello, world")
			}
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotBody["model"] != "gpt-test" {
		t.Errorf("model = %v, want gpt-test", gotBody["model"])
	}
	if gotBody["stream"] != true {
		t.Errorf("stream = %v, want true", gotBody["stream"])
	}
	if want := []string{"Hello", ", ", "world"}; strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("text events = %v, want %v", texts, want)
	}
	if !sawResult {
		t.Error("no KindResult event emitted")
	}
	if res.Text != "Hello, world" {
		t.Errorf("Result.Text = %q, want %q", res.Text, "Hello, world")
	}
	if !res.Success {
		t.Error("Result.Success = false, want true")
	}
}

func TestOpenAIBackendModelOverride(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := sseServer(t, []string{"x"}, &gotPath, &gotBody)
	defer srv.Close()

	b := OpenAIBackend{BaseURL: srv.URL + "/v1", Model: "default-model"}
	if _, err := b.Run(context.Background(), "hi", Options{Model: "override-model"}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotBody["model"] != "override-model" {
		t.Errorf("model = %v, want override-model (opts override)", gotBody["model"])
	}
}

func TestOpenAIBackendAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	b := OpenAIBackend{BaseURL: srv.URL + "/v1", APIKey: "secret-key"}
	if _, err := b.Run(context.Background(), "hi", Options{}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-key")
	}
}

func TestOpenAIBackendContextCancel(t *testing.T) {
	// Server streams slowly so cancellation lands mid-stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for i := 0; i < 100; i++ {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"x"}}]}`+"\n\n")
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	b := OpenAIBackend{BaseURL: srv.URL + "/v1", Model: "m"}
	_, err := b.Run(ctx, "hi", Options{}, nil)
	if err == nil {
		t.Fatal("expected error on context cancellation, got nil")
	}
}

func TestOpenAIBackendErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	b := OpenAIBackend{BaseURL: srv.URL + "/v1", Model: "m"}
	res, err := b.Run(context.Background(), "hi", Options{}, nil)
	if err == nil {
		t.Fatal("expected error on non-200 status, got nil")
	}
	if res.Success {
		t.Error("Result.Success = true on error, want false")
	}
}
