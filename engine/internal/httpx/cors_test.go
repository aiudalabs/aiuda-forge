package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"forge/internal/httpx"
)

// TestCORSLockedOrigin: the default (locked) mode reflects exactly the configured
// origin and allows credentials — never "*".
func TestCORSLockedOrigin(t *testing.T) {
	h := httpx.CORS(httpx.CORSConfig{Origin: "http://localhost:3000"}, http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/runs", nil))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("allow-origin = %q, want the locked origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q, want true", got)
	}
}

// TestCORSDefaultOrigin: empty origin defaults to the console dev origin.
func TestCORSDefaultOrigin(t *testing.T) {
	h := httpx.CORS(httpx.CORSConfig{}, http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/runs", nil))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("default allow-origin = %q, want http://localhost:3000", got)
	}
}

// TestCORSAllowAny: explicit open mode reflects "*" and does NOT set credentials
// (the two are incompatible; a "*" + credentials would be a CSRF master key).
func TestCORSAllowAny(t *testing.T) {
	h := httpx.CORS(httpx.CORSConfig{AllowAny: true}, http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/runs", nil))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow-any allow-origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("allow-any must NOT set credentials, got %q", got)
	}
}

// TestCORSPreflight: OPTIONS is answered with 204 and never reaches next.
func TestCORSPreflight(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := httpx.CORS(httpx.CORSConfig{Origin: "http://localhost:3000"}, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/runs", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if called {
		t.Error("preflight must not reach next handler")
	}
}
