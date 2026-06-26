package httpx

import (
	"crypto/subtle"
	"net/http"
)

// SessionValidator resolves a session bearer token to a stable user id. It is
// satisfied by the auth store (UserForToken). Returning a non-nil error means
// the token is unknown/expired/invalid. httpx defines the interface (rather than
// importing internal/auth) to avoid an import cycle and keep the middleware
// dependency-light.
type SessionValidator interface {
	UserIDForToken(token string) (string, error)
}

// AuthConfig configures the mandatory-auth middleware.
type AuthConfig struct {
	// ServiceToken is the shared bearer the orchestrator presents
	// (VIBEFORGE_API_TOKEN). Empty disables service-token auth.
	ServiceToken string
	// Sessions validates per-user session tokens (the console's login flow).
	// Nil disables session auth (no auth DB configured).
	Sessions SessionValidator
}

// Enabled reports whether any auth mechanism is configured. When false the
// middleware is OPEN — every request passes. The control plane must only bind to
// loopback in that case (see cmd/control).
func (c AuthConfig) Enabled() bool {
	return c.ServiceToken != "" || c.Sessions != nil
}

// publicPath reports whether path+method is reachable WITHOUT auth. Only the
// liveness probes and the login endpoint are public; everything else — including
// every GET read — requires auth (audit C1/C4: unauth reads leak secrets).
func publicPath(method, path string) bool {
	switch path {
	case "/healthz", "/readyz":
		return true
	case "/auth/login":
		return method == http.MethodPost
	}
	return false
}

// Auth wraps next with MANDATORY authentication when cfg.Enabled(). A request is
// allowed iff it carries EITHER a valid session bearer token OR the exact service
// token. Public paths (liveness, POST /auth/login) and CORS preflight (OPTIONS)
// are always exempt. Rejected requests get 401.
//
// When cfg is not Enabled() the middleware is a transparent pass-through; the
// operator is responsible for binding to loopback only in that mode.
//
// Auth sits INSIDE the CORS wrapper so CORS answers OPTIONS before Auth runs:
//
//	httpx.CORS(corsCfg, httpx.Auth(cfg, server))
func Auth(cfg AuthConfig, next http.Handler) http.Handler {
	if !cfg.Enabled() {
		return next // open mode — caller must bind loopback-only
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || publicPath(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if cfg.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="vibeforge"`)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	})
}

// authorized reports whether r presents valid credentials: the service token
// (constant-time compared) or a live session token.
func (c AuthConfig) authorized(r *http.Request) bool {
	tok := bearerToken(r)
	// Browser WebSockets cannot set an Authorization header, so the WS upgrade
	// (and only that path) may carry the token as a ?token= query param. This is
	// the standard accepted pattern; the token is still validated identically.
	if tok == "" && r.URL.Path == "/ws" {
		tok = r.URL.Query().Get("token")
	}
	if tok == "" {
		return false
	}
	// Service token: constant-time compare so a wrong token can't be timed out.
	if c.ServiceToken != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(c.ServiceToken)) == 1 {
		return true
	}
	// Session token: look it up in the auth store.
	if c.Sessions != nil {
		if _, err := c.Sessions.UserIDForToken(tok); err == nil {
			return true
		}
	}
	return false
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header,
// or "" if absent/malformed.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return ""
	}
	return h[len(prefix):]
}
