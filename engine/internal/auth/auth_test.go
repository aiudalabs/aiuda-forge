package auth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndAuthenticate(t *testing.T) {
	s := openTest(t)

	if _, err := s.CreateUser("Admin@Example.com", "hunter2"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Wrong password → ErrBadCredential.
	if _, _, err := s.Authenticate("admin@example.com", "wrong"); err != ErrBadCredential {
		t.Errorf("wrong password: want ErrBadCredential, got %v", err)
	}

	// Unknown email → ErrBadCredential (not a different error that leaks existence).
	if _, _, err := s.Authenticate("nobody@example.com", "hunter2"); err != ErrBadCredential {
		t.Errorf("unknown email: want ErrBadCredential, got %v", err)
	}

	// Correct credentials (case-insensitive email) → session token.
	u, sess, err := s.Authenticate("ADMIN@example.com", "hunter2")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if u.Email != "admin@example.com" {
		t.Errorf("email not normalized: %q", u.Email)
	}
	if len(sess.Token) != 64 {
		t.Errorf("token length = %d, want 64 hex chars", len(sess.Token))
	}

	// The token resolves back to the user.
	got, err := s.UserForToken(sess.Token)
	if err != nil {
		t.Fatalf("UserForToken: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("token resolved to wrong user: %s != %s", got.ID, u.ID)
	}
}

func TestDuplicateEmail(t *testing.T) {
	s := openTest(t)
	if _, err := s.CreateUser("a@b.com", "pw"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateUser("A@B.com", "pw2"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate (case-folded) email: want ErrExists, got %v", err)
	}
}

func TestLogoutInvalidatesToken(t *testing.T) {
	s := openTest(t)
	_, _ = s.CreateUser("a@b.com", "pw")
	_, sess, _ := s.Authenticate("a@b.com", "pw")

	if err := s.DeleteSession(sess.Token); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.UserForToken(sess.Token); err != ErrNotFound {
		t.Errorf("after logout: want ErrNotFound, got %v", err)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	s := openTest(t)
	_, _ = s.CreateUser("a@b.com", "pw")

	// Freeze "now" in the past so the minted session is already expired.
	past := time.Now().Add(-2 * SessionTTL)
	s.Now = func() time.Time { return past }
	_, sess, _ := s.Authenticate("a@b.com", "pw")

	// Move "now" back to the present: the token is past its expiry.
	s.Now = time.Now
	if _, err := s.UserForToken(sess.Token); err != ErrNotFound {
		t.Errorf("expired token: want ErrNotFound, got %v", err)
	}
}

func TestUnknownAndEmptyToken(t *testing.T) {
	s := openTest(t)
	for _, tok := range []string{"", "deadbeef"} {
		if _, err := s.UserForToken(tok); err != ErrNotFound {
			t.Errorf("token %q: want ErrNotFound, got %v", tok, err)
		}
	}
}
