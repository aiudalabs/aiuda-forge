package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"forge/internal/channels"
)

// captureDoer records the last request and returns a canned response.
type captureDoer struct {
	lastURL  string
	lastBody string
	status   int
}

func (c *captureDoer) do(req *http.Request) (*http.Response, error) {
	c.lastURL = req.URL.String()
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		c.lastBody = string(b)
	}
	status := c.status
	if status == 0 {
		status = 200
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
}

func TestNotifySendsMessage(t *testing.T) {
	cd := &captureDoer{}
	c := New(func() string { return "BOT123" })
	c.doer = cd.do
	c.base = "https://tg.test"

	err := c.Notify(context.Background(), "chat-9", channels.Event{Title: "❌ Run falló", Detail: "run r1"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !strings.Contains(cd.lastURL, "/botBOT123/sendMessage") {
		t.Fatalf("url = %q, want bot token + sendMessage", cd.lastURL)
	}
	if !strings.Contains(cd.lastBody, "chat_id=chat-9") {
		t.Fatalf("body missing chat_id: %q", cd.lastBody)
	}
	if !strings.Contains(cd.lastBody, "Run") {
		t.Fatalf("body missing message text: %q", cd.lastBody)
	}
}

func TestNotifyRequiresToken(t *testing.T) {
	c := New(func() string { return "" }) // no token configured
	err := c.Notify(context.Background(), "chat-9", channels.Event{Title: "x"})
	if err == nil {
		t.Fatal("expected error when bot token is not configured")
	}
}

func TestNotifyPropagatesAPIError(t *testing.T) {
	cd := &captureDoer{status: 400}
	c := New(func() string { return "BOT" })
	c.doer = cd.do
	c.base = "https://tg.test"
	if err := c.Notify(context.Background(), "chat", channels.Event{Title: "x"}); err == nil {
		t.Fatal("expected error on non-2xx telegram response")
	}
}
