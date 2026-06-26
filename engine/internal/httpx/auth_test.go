package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"forge/internal/httpx"
)

func okHandler(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// fakeSessions is a tiny SessionValidator: any token in valid resolves.
type fakeSessions map[string]string

func (f fakeSessions) UserIDForToken(token string) (string, error) {
	if uid, ok := f[token]; ok {
		return uid, nil
	}
	return "", errors.New("not found")
}

// TestAuthOpen: with no service token and no session validator, auth is OPEN —
// every request passes (loopback-only mode; caller binds 127.0.0.1).
func TestAuthOpen(t *testing.T) {
	h := httpx.Auth(httpx.AuthConfig{}, http.HandlerFunc(okHandler))
	for _, path := range []string{"/runs", "/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("open mode: %s expected 200, got %d", path, rec.Code)
		}
	}
}

// TestAuthServiceToken: with a service token set, wrong/missing bearer → 401;
// correct → passes. This is the orchestrator's path.
func TestAuthServiceToken(t *testing.T) {
	h := httpx.Auth(httpx.AuthConfig{ServiceToken: "secret"}, http.HandlerFunc(okHandler))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/runs", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: expected 401, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/runs", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/runs", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("correct token: expected 200, got %d", rec.Code)
	}
}

// TestAuthSessionToken: a valid session bearer is accepted; an unknown one is 401.
func TestAuthSessionToken(t *testing.T) {
	h := httpx.Auth(httpx.AuthConfig{Sessions: fakeSessions{"good": "usr-1"}}, http.HandlerFunc(okHandler))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/runs", nil)
	req.Header.Set("Authorization", "Bearer good")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("valid session: expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/runs", nil)
	req.Header.Set("Authorization", "Bearer bad")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad session: expected 401, got %d", rec.Code)
	}
}

// TestAuthExemptions: liveness probes, POST /auth/login, and OPTIONS are exempt
// even when auth is mandatory. A GET to /auth/login is NOT exempt (only POST).
func TestAuthExemptions(t *testing.T) {
	h := httpx.Auth(httpx.AuthConfig{ServiceToken: "secret"}, http.HandlerFunc(okHandler))

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s without token: expected 200, got %d", path, rec.Code)
		}
	}

	// POST /auth/login is public.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("POST /auth/login: expected 200 (public), got %d", rec.Code)
	}

	// GET /auth/login is NOT public.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /auth/login: expected 401, got %d", rec.Code)
	}

	// OPTIONS preflight carries no auth header and must pass through.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/runs", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS without token: expected 200, got %d", rec.Code)
	}
}
