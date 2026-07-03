package ghapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTrigger records SyncRepo calls. Because the handler fires SyncRepo in a
// goroutine, calls are guarded by a mutex and observable via a buffered channel.
type fakeTrigger struct {
	mu   sync.Mutex
	urls []string
	ch   chan string
}

func newFakeTrigger() *fakeTrigger {
	return &fakeTrigger{ch: make(chan string, 8)}
}

func (f *fakeTrigger) SyncRepo(url string) {
	f.mu.Lock()
	f.urls = append(f.urls, url)
	f.mu.Unlock()
	f.ch <- url
}

// awaitURL waits briefly for a SyncRepo call, returning the url and whether one
// arrived. Used to synchronize on the handler's goroutine.
func (f *fakeTrigger) awaitURL(t *testing.T) (string, bool) {
	t.Helper()
	select {
	case url := <-f.ch:
		return url, true
	case <-time.After(time.Second):
		return "", false
	}
}

// expectNoURL asserts no SyncRepo call arrives within a short window.
func (f *fakeTrigger) expectNoURL(t *testing.T) {
	t.Helper()
	select {
	case url := <-f.ch:
		t.Fatalf("unexpected SyncRepo(%q)", url)
	case <-time.After(100 * time.Millisecond):
	}
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

const testSecret = "s3cr3t"

// req builds a signed webhook request. Pass sig="" to omit the signature header.
func req(event, delivery, sig string, body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
	r.Header.Set("X-GitHub-Event", event)
	if delivery != "" {
		r.Header.Set("X-GitHub-Delivery", delivery)
	}
	if sig != "" {
		r.Header.Set("X-Hub-Signature-256", sig)
	}
	return r
}

const repoBody = `{"action":"opened","repository":{"html_url":"https://github.com/acme/widgets"}}`

func TestWebhookHandler_SingleRequest(t *testing.T) {
	tests := []struct {
		name       string
		secret     string
		event      string
		delivery   string
		body       string
		signWith   string // "" → no signature header
		wantStatus int
		wantURL    string // "" → expect no SyncRepo call
	}{
		{
			name:       "empty secret → 503",
			secret:     "",
			event:      "issues",
			delivery:   "d1",
			body:       repoBody,
			signWith:   testSecret,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "missing signature → 401",
			secret:     testSecret,
			event:      "issues",
			delivery:   "d2",
			body:       repoBody,
			signWith:   "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong signature → 401",
			secret:     testSecret,
			event:      "issues",
			delivery:   "d3",
			body:       repoBody,
			signWith:   "wrong-secret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid issues → 202 and trigger",
			secret:     testSecret,
			event:      "issues",
			delivery:   "d4",
			body:       repoBody,
			signWith:   testSecret,
			wantStatus: http.StatusAccepted,
			wantURL:    "https://github.com/acme/widgets",
		},
		{
			name:       "ping → 200 no trigger",
			secret:     testSecret,
			event:      "ping",
			delivery:   "d5",
			body:       `{"zen":"Keep it simple"}`,
			signWith:   testSecret,
			wantStatus: http.StatusOK,
		},
		{
			name:       "unknown event → 200 ignored",
			secret:     testSecret,
			event:      "star",
			delivery:   "d6",
			body:       repoBody,
			signWith:   testSecret,
			wantStatus: http.StatusOK,
		},
		{
			name:       "relevant event without repo url → 202 no trigger",
			secret:     testSecret,
			event:      "pull_request",
			delivery:   "d7",
			body:       `{"action":"opened"}`,
			signWith:   testSecret,
			wantStatus: http.StatusAccepted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger := newFakeTrigger()
			h := WebhookHandler(func() string { return tt.secret }, trigger)

			body := []byte(tt.body)
			sig := ""
			if tt.signWith != "" {
				sig = sign(tt.signWith, body)
			}
			rec := httptest.NewRecorder()
			h(rec, req(tt.event, tt.delivery, sig, body))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantURL != "" {
				url, ok := trigger.awaitURL(t)
				if !ok {
					t.Fatalf("expected SyncRepo(%q), got none", tt.wantURL)
				}
				if url != tt.wantURL {
					t.Fatalf("SyncRepo url = %q, want %q", url, tt.wantURL)
				}
			} else {
				trigger.expectNoURL(t)
			}
		})
	}
}

func TestWebhookHandler_DuplicateDelivery(t *testing.T) {
	trigger := newFakeTrigger()
	h := WebhookHandler(func() string { return testSecret }, trigger)

	body := []byte(repoBody)
	sig := sign(testSecret, body)

	// First delivery fires the trigger.
	rec1 := httptest.NewRecorder()
	h(rec1, req("issues", "dup-1", sig, body))
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", rec1.Code)
	}
	if _, ok := trigger.awaitURL(t); !ok {
		t.Fatal("expected SyncRepo on first delivery")
	}

	// Same delivery id → 200 duplicate, no second trigger.
	rec2 := httptest.NewRecorder()
	h(rec2, req("issues", "dup-1", sig, body))
	if rec2.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "duplicate") {
		t.Fatalf("duplicate body = %q, want it to mention duplicate", rec2.Body.String())
	}
	trigger.expectNoURL(t)
}
