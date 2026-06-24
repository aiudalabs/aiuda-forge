package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"vibeforge-kernel/internal/httpx"
)

func okHandler(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// TestAuthOpen: when no token is configured, all requests pass through.
func TestAuthOpen(t *testing.T) {
	h := httpx.Auth("", http.HandlerFunc(okHandler))
	for _, path := range []string{"/runs", "/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("open mode: %s expected 200, got %d", path, rec.Code)
		}
	}
}

// TestAuthEnabled: with a token set, wrong/missing bearer → 401; correct → passes.
func TestAuthEnabled(t *testing.T) {
	h := httpx.Auth("secret", http.HandlerFunc(okHandler))

	// No Authorization header → 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/runs", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: expected 401, got %d", rec.Code)
	}

	// Wrong token → 401.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/runs", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", rec.Code)
	}

	// Correct token → 200.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/runs", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("correct token: expected 200, got %d", rec.Code)
	}
}

// TestAuthExemptions: /healthz and /readyz and OPTIONS are always exempt.
func TestAuthExemptions(t *testing.T) {
	h := httpx.Auth("secret", http.HandlerFunc(okHandler))

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s without token: expected 200, got %d", path, rec.Code)
		}
	}

	// OPTIONS preflight carries no auth header and must pass through.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/runs", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS without token: expected 200, got %d", rec.Code)
	}
}
