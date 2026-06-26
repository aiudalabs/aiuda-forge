// Package httpx holds small HTTP middlewares shared by the control-plane,
// orchestrator and studio servers.
package httpx

import "net/http"

// CORSConfig configures the CORS middleware.
type CORSConfig struct {
	// Origin is the single allowed origin reflected in
	// Access-Control-Allow-Origin (e.g. "http://localhost:3000"). Empty defaults
	// to "http://localhost:3000".
	Origin string
	// AllowAny opens CORS to any origin ("*"). This is an explicit dev/open-mode
	// opt-in only; it is INCOMPATIBLE with credentialed auth and must never be
	// combined with a non-loopback bind. When false (the default), exactly one
	// origin is reflected and credentials are allowed.
	AllowAny bool
}

// CORS wraps next with CORS headers. By default it reflects exactly ONE origin
// (cfg.Origin) and sets Access-Control-Allow-Credentials, which is the safe
// posture when auth is active (audit C1: never "*" with credentials — that is a
// CSRF master key). Set cfg.AllowAny only for an explicitly-opened dev mode.
// Preflight OPTIONS requests are answered here and never reach next.
func CORS(cfg CORSConfig, next http.Handler) http.Handler {
	origin := cfg.Origin
	if origin == "" {
		origin = "http://localhost:3000"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if cfg.AllowAny {
			// Open mode: any origin, but credentials are NOT allowed with "*"
			// (the spec forbids it, and we must not arm CSRF). The browser will
			// reject credentialed requests, which is the intended safety net.
			h.Set("Access-Control-Allow-Origin", "*")
		} else {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
		}
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		h.Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
