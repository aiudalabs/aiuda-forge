package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/auth"
)

func authServer(t *testing.T) *Server {
	t.Helper()
	au, err := auth.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	t.Cleanup(func() { au.Close() })
	return &Server{Auth: au}
}

func postRegister(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/auth/register", strings.NewReader(body))
	s.register(rec, req)
	return rec
}

// A valid signup returns 201 with a session token + user, and the user can then log in.
func TestRegisterCreatesUserAndSession(t *testing.T) {
	s := authServer(t)
	rec := postRegister(t, s, `{"email":"NEW@Example.com","password":"hunter2pass"}`)
	if rec.Code != 201 {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Token == "" {
		t.Error("expected a session token")
	}
	if out.User.Email != "new@example.com" {
		t.Errorf("email = %q, want normalized new@example.com", out.User.Email)
	}
	// The token resolves to the new user.
	if _, err := s.Auth.UserForToken(out.Token); err != nil {
		t.Errorf("token should resolve to user: %v", err)
	}
}

// A duplicate email is 409 (no second account, no leak of internals).
func TestRegisterDuplicateEmail(t *testing.T) {
	s := authServer(t)
	if _, err := s.Auth.CreateUser("dupe@example.com", "password123"); err != nil {
		t.Fatal(err)
	}
	rec := postRegister(t, s, `{"email":"dupe@example.com","password":"password123"}`)
	if rec.Code != 409 {
		t.Fatalf("duplicate status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// A short password is rejected with 400 before any user is created.
func TestRegisterWeakPassword(t *testing.T) {
	s := authServer(t)
	rec := postRegister(t, s, `{"email":"weak@example.com","password":"short"}`)
	if rec.Code != 400 {
		t.Fatalf("weak-password status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if n, _ := s.Auth.CountUsers(); n != 0 {
		t.Errorf("no user should be created on weak password, got %d", n)
	}
}
