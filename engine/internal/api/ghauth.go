package api

// Onboarding GitHub (Forja, GTM 2026-07-03):
//   GET /setup/github-app          — página del operador: crea la App vía manifest flow
//   GET /setup/github-app/callback — canjea el code → credenciales persistidas
//   GET /auth/github/start         — "Continue with GitHub" (OAuth user-to-server)
//   GET /auth/github/callback      — code→token, enlaza/crea usuario, sesión, → consola
//   GET /auth/github/status        — (auth) ¿este usuario tiene GitHub conectado?
// El manifest flow se auto-desactiva (404) cuando la App ya está configurada.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"forge/internal/auth"
	"forge/internal/ghapp"
	"forge/internal/httpx"
)

// ghAppCredsPath resuelve dónde viven las credenciales de la App (junto al
// settings.json del registry).
func (s *Server) ghAppCredsPath() string {
	if p := os.Getenv("VIBEFORGE_GHAPP_CREDS"); p != "" {
		return p
	}
	return "github-app.json"
}

func (s *Server) publicBaseURL() string {
	if u := os.Getenv("VIBEFORGE_PUBLIC_URL"); u != "" {
		return u
	}
	return "http://localhost:8080"
}

func (s *Server) consoleURL() string {
	if u := os.Getenv("VIBEFORGE_CONSOLE_URL"); u != "" {
		return u
	}
	return "http://localhost:3000"
}

// oauthStates: anti-CSRF del flujo OAuth (instancia única → memoria).
var (
	oauthMu     sync.Mutex
	oauthStates = map[string]time.Time{}
)

func newOAuthState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	st := hex.EncodeToString(b)
	oauthMu.Lock()
	defer oauthMu.Unlock()
	// GC de states viejos
	for k, t := range oauthStates {
		if time.Since(t) > 15*time.Minute {
			delete(oauthStates, k)
		}
	}
	oauthStates[st] = time.Now()
	return st, nil
}

func consumeOAuthState(st string) bool {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	t, ok := oauthStates[st]
	delete(oauthStates, st)
	return ok && time.Since(t) <= 15*time.Minute
}

// GET /setup/github-app?org=aiudalabs — página que auto-envía el manifest a
// GitHub. Solo disponible mientras la App NO esté configurada.
func (s *Server) setupGitHubApp(w http.ResponseWriter, r *http.Request) {
	creds, err := ghapp.LoadCredentials(s.ghAppCredsPath())
	if err == nil && creds.Configured() {
		httpErr(w, http.StatusNotFound, "GitHub App already configured")
		return
	}
	org := r.URL.Query().Get("org")
	if org == "" {
		org = "aiudalabs"
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "Forja"
	}
	manifest, err := json.Marshal(ghapp.Manifest(name, org, s.publicBaseURL(), s.consoleURL()))
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	action := fmt.Sprintf("https://github.com/organizations/%s/settings/apps/new", html.EscapeString(org))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Crear GitHub App Forja</title>
<body style="font-family:system-ui;max-width:640px;margin:80px auto;line-height:1.5">
<h1>Crear la GitHub App «%s» en <code>%s</code></h1>
<p>Al continuar, GitHub te pedirá aprobar la App con los permisos que el conductor
necesita (repos, issues, PRs, actions, secrets). Un click y las credenciales
quedan guardadas solas — sin copy-paste de secretos.</p>
<form action="%s" method="post">
<input type="hidden" name="manifest" value="%s">
<button type="submit" style="font-size:16px;padding:10px 22px;cursor:pointer">Crear App en GitHub →</button>
</form></body>`, html.EscapeString(name), html.EscapeString(org), action, html.EscapeString(string(manifest)))
}

// GET /setup/github-app/callback?code= — canje del manifest code.
func (s *Server) setupGitHubAppCallback(w http.ResponseWriter, r *http.Request) {
	existing, err := ghapp.LoadCredentials(s.ghAppCredsPath())
	if err == nil && existing.Configured() {
		httpErr(w, http.StatusNotFound, "GitHub App already configured")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		httpErr(w, http.StatusBadRequest, "missing code")
		return
	}
	creds, err := ghapp.ConvertManifest(r.Context(), code)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := ghapp.SaveCredentials(s.ghAppCredsPath(), creds); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Forja conectada</title>
<body style="font-family:system-ui;max-width:640px;margin:80px auto;line-height:1.5">
<h1>✓ App «%s» creada en %s</h1>
<p>Credenciales guardadas. Siguiente paso: <a href="%s/installations/new">instalar la App
en la org</a> (elige los repos), y después los usuarios ya pueden entrar con
«Continue with GitHub» en la consola.</p>`,
		html.EscapeString(creds.Slug), html.EscapeString(creds.Org), html.EscapeString(creds.HTMLURL))
}

// GET /auth/github/start — redirige al authorize de la App.
func (s *Server) githubAuthStart(w http.ResponseWriter, r *http.Request) {
	creds, err := ghapp.LoadCredentials(s.ghAppCredsPath())
	if err != nil || !creds.Configured() {
		httpErr(w, http.StatusServiceUnavailable, "GitHub App not configured — run /setup/github-app first")
		return
	}
	state, err := newOAuthState()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	url := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s/auth/github/callback&state=%s",
		creds.ClientID, s.publicBaseURL(), state)
	http.Redirect(w, r, url, http.StatusFound)
}

// GET /auth/github/callback — canjea el code, enlaza/crea el usuario local,
// guarda el token user-to-server y entrega la sesión a la consola por fragment
// (#gh_session=…): el fragment nunca viaja a servidores ni queda en logs.
func (s *Server) githubAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		httpErr(w, http.StatusServiceUnavailable, "auth store not configured")
		return
	}
	// Dos caminos legítimos llegan aquí:
	//  a) Login iniciado por nosotros (/auth/github/start) → trae NUESTRO state.
	//  b) Autorización durante la INSTALACIÓN de la App
	//     (request_oauth_on_install): GitHub redirige con code +
	//     setup_action=install|update y SIN state nuestro.
	// Solo (a) puede emitir sesión. Sin state no hay prueba anti-CSRF de que ESTE
	// browser inició el login: canjear el code y emitir sesión aquí sería login-
	// CSRF (A1) — un atacante manda a la víctima su propio callback code+
	// setup_action y el browser de la víctima queda con una sesión ligada a la
	// cuenta GitHub del atacante (y viceversa, enlaza tokens ajenos). El camino
	// (b) se trata como instalación-OK SIN autenticar: redirigimos a la consola
	// con un flag y el usuario entra por «Continue with GitHub» (con state).
	state := r.URL.Query().Get("state")
	setupAction := r.URL.Query().Get("setup_action")
	if state == "" && setupAction == "" {
		httpErr(w, http.StatusForbidden, "invalid OAuth callback (no state, no setup_action)")
		return
	}
	if state == "" {
		http.Redirect(w, r,
			fmt.Sprintf("%s/login?gh_setup=%s", s.consoleURL(), url.QueryEscape(setupAction)),
			http.StatusFound)
		return
	}
	if !consumeOAuthState(state) {
		httpErr(w, http.StatusForbidden, "sesión de login caducada — vuelve a la consola y pulsa 'Continuar con GitHub' de nuevo")
		return
	}
	creds, err := ghapp.LoadCredentials(s.ghAppCredsPath())
	if err != nil || !creds.Configured() {
		httpErr(w, http.StatusServiceUnavailable, "GitHub App not configured")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		httpErr(w, http.StatusBadRequest, "missing code")
		return
	}
	tok, err := ghapp.ExchangeOAuthCode(r.Context(), creds.ClientID, creds.ClientSecret, code)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ghUser, err := ghapp.FetchUser(r.Context(), tok.AccessToken)
	if err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	user, err := s.Auth.EnsureGitHubUser(fmt.Sprintf("%d", ghUser.ID), ghUser.Login, ghUser.Email)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var expiresAt int64
	if tok.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UnixMilli()
	}
	if err := s.Auth.SetGitHubToken(user.ID, auth.GitHubToken{
		Login: ghUser.Login, AccessToken: tok.AccessToken,
		RefreshToken: tok.RefreshToken, ExpiresAt: expiresAt,
	}); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess, err := s.Auth.SessionForUser(user.ID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r,
		fmt.Sprintf("%s/login#gh_session=%s&email=%s", s.consoleURL(), sess.Token, user.Email),
		http.StatusFound)
}

// GET /auth/github/status — ¿el usuario actual tiene GitHub conectado?
func (s *Server) githubAuthStatus(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	uid := httpx.UserIDFromContext(r.Context())
	if uid == "" {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	t, ok, err := s.Auth.GitHubTokenFor(uid)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	creds, _ := ghapp.LoadCredentials(s.ghAppCredsPath())
	writeJSON(w, http.StatusOK, map[string]any{
		"connected":      ok,
		"login":          t.Login,
		"app_configured": creds.Configured(),
		"app_url":        creds.HTMLURL,
	})
}
