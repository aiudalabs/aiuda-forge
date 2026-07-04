package api

// A1 (login CSRF): el callback OAuth sin NUESTRO state jamás emite sesión.
// El camino de instalación (code+setup_action sin state) redirige a la consola
// como instalación-OK sin autenticar; sin state ni setup_action es 403.

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/auth"
)

func ghAuthServer(t *testing.T) *Server {
	t.Helper()
	au, err := auth.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("auth.Open: %v", err)
	}
	t.Cleanup(func() { au.Close() })
	return &Server{Auth: au, linkCodes: newLinkCodeStore()}
}

// (d) code+setup_action SIN state → NO hay sesión: redirect de instalación-OK
// a la consola, sin canjear el code ni tocar el auth store.
func TestGitHubCallbackInstallWithoutStateEmitsNoSession(t *testing.T) {
	t.Setenv("VIBEFORGE_CONSOLE_URL", "http://console.test")
	s := ghAuthServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/github/callback?code=attacker-code&setup_action=install", nil)
	s.githubAuthCallback(rec, req)

	if rec.Code != 302 {
		t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc != "http://console.test/login?gh_setup=install" {
		t.Fatalf("Location = %q, want the install-ok redirect", loc)
	}
	if strings.Contains(loc, "gh_session") {
		t.Fatalf("Location %q carries a session — login CSRF regression", loc)
	}
}

// Sin state y sin setup_action el callback es inválido: 403, sin sesión.
func TestGitHubCallbackWithoutStateOrSetupActionForbidden(t *testing.T) {
	s := ghAuthServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/github/callback?code=attacker-code", nil)
	s.githubAuthCallback(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// Un state desconocido/caducado también es 403 (el camino de login exige el
// state emitido por /auth/github/start).
func TestGitHubCallbackUnknownStateForbidden(t *testing.T) {
	s := ghAuthServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/github/callback?code=x&state=forged", nil)
	s.githubAuthCallback(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}
