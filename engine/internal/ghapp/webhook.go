// Package ghapp holds the inbound GitHub App integration: the webhook receiver
// that turns GitHub events (issues, PRs, checks…) into repo-sync triggers.
package ghapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
)

// maxBody caps the request body we read before verifying the signature.
const maxBody = 1 << 20 // 1 MiB

// maxSeenDeliveries bounds the dedupe set so a long-lived receiver does not
// leak memory; oldest delivery ids are evicted FIFO once the cap is reached.
const maxSeenDeliveries = 4096

// SyncTrigger es lo que el receptor dispara al llegar un evento relevante.
// La implementación (el conductor) decide async/cola; esto debe retornar rápido.
type SyncTrigger interface {
	SyncRepo(repoURL string)
}

// eventsThatSync are the GitHub event types that warrant a repo sync. Anything
// else is acknowledged (200) but ignored.
var eventsThatSync = map[string]bool{
	"issues":             true,
	"pull_request":       true,
	"issue_dependencies": true,
	"workflow_run":       true,
	"check_run":          true,
	"issue_comment":      true,
}

// dedupe is a thread-safe, bounded FIFO set of delivery ids. A GitHub delivery
// id seen before must not re-trigger a sync (GitHub retries on timeout/5xx).
type dedupe struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	order []string
}

func newDedupe() *dedupe {
	return &dedupe{seen: make(map[string]struct{})}
}

// add records id and reports whether it was already present. A blank id is
// never deduped (we cannot key on it) — treat it as always-fresh.
func (d *dedupe) add(id string) (dup bool) {
	if id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[id]; ok {
		return true
	}
	d.seen[id] = struct{}{}
	d.order = append(d.order, id)
	if len(d.order) > maxSeenDeliveries {
		evict := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, evict)
	}
	return false
}

// WebhookHandler devuelve el http.HandlerFunc para POST /webhooks/github.
//
// secret() se evalúa por-request (hot-reload desde settings). Verifica la firma
// HMAC-SHA256 del header X-Hub-Signature-256 en tiempo constante, deduplica por
// X-GitHub-Delivery y, para eventos relevantes, dispara trigger.SyncRepo en una
// goroutine para responder rápido.
func WebhookHandler(secret func() string, trigger SyncTrigger) http.HandlerFunc {
	seen := newDedupe()
	return func(w http.ResponseWriter, r *http.Request) {
		sec := secret()
		if sec == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "github webhook not configured"})
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "body too large"})
			return
		}

		if !validSignature(sec, r.Header.Get("X-Hub-Signature-256"), body) {
			log.Printf("ghapp: invalid webhook signature (delivery=%q event=%q)",
				r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event"))
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid signature"})
			return
		}

		if seen.add(r.Header.Get("X-GitHub-Delivery")) {
			writeJSON(w, http.StatusOK, map[string]any{"duplicate": true})
			return
		}

		event := r.Header.Get("X-GitHub-Event")
		if event == "ping" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		if !eventsThatSync[event] {
			writeJSON(w, http.StatusOK, map[string]any{"ignored": true})
			return
		}

		url := repoURL(body)
		if url == "" {
			// Relevant event but no repository we can act on: acknowledge and drop.
			writeJSON(w, http.StatusAccepted, map[string]any{"queued": false})
			return
		}
		go trigger.SyncRepo(url)
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
	}
}

// validSignature reports whether header == "sha256=" + hex(HMAC-SHA256(secret, body)),
// compared in constant time. A missing or malformed header fails closed.
func validSignature(secret, header string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(want, mac.Sum(nil))
}

// repoURL extracts repository.html_url from a webhook payload, or "" if absent.
func repoURL(body []byte) string {
	var payload struct {
		Repository struct {
			HTMLURL string `json:"html_url"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Repository.HTMLURL
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
