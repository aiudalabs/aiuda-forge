package httpx

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

// ctxKey is the private context key type for values httpx stashes on a request.
type ctxKey int

const userIDKey ctxKey = iota

// WithUserID returns a copy of ctx carrying the authenticated user id. Exported
// so tests can populate it; the middleware calls it after a session validates.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext returns the authenticated user id stashed on the request, or
// "" when the request was authorized by the service token (no per-user session)
// or auth is open. Handlers that need an owner (POST/GET /projects) read it here.
func UserIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

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
	// PreviewSecret is the HMAC key that signs/verifies preview capability tokens.
	// A preview is served under /pv/{token}/… and authorized SOLELY by that
	// path-embedded token — never a session or service token — because a preview
	// runs untrusted repo JS that could otherwise replay a leaked token against the
	// API (audit C2). The token rides in the PATH (not a query or cookie) so the
	// browser carries it automatically on every relative subresource request; a
	// cookie cannot, because the CSP-sandbox opaque origin drops SameSite cookies on
	// subresources (verified empirically). Empty leaves /pv unauthorizable (401).
	PreviewSecret []byte
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
	case "/auth/login", "/auth/register":
		return method == http.MethodPost
	case "/webhooks/telegram":
		// Public route, but authenticated by the Telegram bot secret header
		// (verified in the handler), not the session/service token.
		return method == http.MethodPost
	case "/webhooks/github":
		// Firmado por HMAC X-Hub-Signature-256 (verificado en el handler).
		return method == http.MethodPost
	case "/auth/github/start", "/auth/github/callback":
		// Flujo OAuth de browser: llegan por redirect sin bearer. El callback
		// se protege con el state anti-CSRF; el start solo redirige a GitHub.
		return method == http.MethodGet
	case "/setup/github-app", "/setup/github-app/callback":
		// Manifest flow del operador: el handler se auto-desactiva (404) en
		// cuanto la App queda configurada.
		return method == http.MethodGet
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
		// /pv is the preview-serving origin: authorized ONLY by the path-embedded
		// preview token (never a session/service token), because it serves untrusted
		// repo JS. The token in the path means subresources carry it automatically.
		if strings.HasPrefix(r.URL.Path, "/pv/") {
			if cfg.authorizePreview(r) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="vibeforge-preview"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		ok, userID := cfg.authorize(r)
		if ok {
			// Stash the resolved user id (empty for a service-token caller) so
			// owner-scoped handlers can read it from the request context.
			if userID != "" {
				r = r.WithContext(WithUserID(r.Context(), userID))
			}
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="vibeforge"`)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	})
}

// authorize reports whether r presents valid credentials and, for a session
// token, the resolved user id. The service token authorizes with userID="" (no
// per-user identity). A failed auth returns (false, "").
func (c AuthConfig) authorize(r *http.Request) (ok bool, userID string) {
	tok := bearerToken(r)
	// Browser WebSockets cannot set an Authorization header, so the WS upgrade
	// (and only that path) may carry the token as a ?token= query param. This is
	// the standard accepted pattern; the token is still validated identically.
	// (/previews does NOT fall through here — it has its own preview-token path in
	// Auth so a session/service token is never accepted by query for a preview.)
	if tok == "" && r.URL.Path == "/ws" {
		tok = r.URL.Query().Get("token")
	}
	if tok == "" {
		return false, ""
	}
	// Service token: constant-time compare so a wrong token can't be timed out.
	if c.ServiceToken != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(c.ServiceToken)) == 1 {
		return true, ""
	}
	// Session token: look it up in the auth store and carry the user id forward.
	if c.Sessions != nil {
		if id, err := c.Sessions.UserIDForToken(tok); err == nil {
			return true, id
		}
	}
	return false, ""
}

// PreviewTokenFromPath extracts the token segment from a /pv/{token}/… path, or ""
// if p is not a /pv path. Exported so the serving handler resolves the same segment.
func PreviewTokenFromPath(p string) string {
	rest := strings.TrimPrefix(p, "/pv/")
	if rest == p {
		return "" // not a /pv path
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// authorizePreview reports whether the /pv/{token}/… request carries a valid,
// unexpired preview token in its path. The token itself is the capability — it
// encodes the one preview it authorizes, so a valid token can only ever serve its
// own project/run (the handler re-decodes it). A session or service token is NEVER
// accepted here: untrusted preview JS must not be able to replay a broad token.
func (c AuthConfig) authorizePreview(r *http.Request) bool {
	if len(c.PreviewSecret) == 0 {
		return false // previews not configured for authorization
	}
	tok := PreviewTokenFromPath(r.URL.Path)
	if tok == "" {
		return false
	}
	_, _, err := VerifyPreviewToken(c.PreviewSecret, tok, time.Now())
	return err == nil
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
