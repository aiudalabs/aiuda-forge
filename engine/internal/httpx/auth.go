package httpx

import "net/http"

// Auth wraps next with opt-in bearer-token authentication. When token is empty
// the middleware is a no-op (open, local-dev default — demo works with no config).
// When token is set, every request must carry "Authorization: Bearer <token>" or
// receive a 401; /healthz and /readyz are always exempt (liveness probes must not
// need auth); OPTIONS is always exempt (CORS preflight carries no auth header).
//
// Auth is designed to sit INSIDE the CORS wrapper so the order in main.go is:
//
//	httpx.CORS(origin, httpx.Auth(token, a.Server))
//
// That ordering means CORS handles OPTIONS before Auth ever sees it, but Auth also
// short-circuits OPTIONS independently for defence-in-depth. The §C internal
// endpoints (/runs/claim, /steps/*/report|heartbeat|usage) share the same mux and
// are therefore covered by this single middleware without additional wiring.
func Auth(token string, next http.Handler) http.Handler {
	// No token configured → open; pass every request straight through.
	if token == "" {
		return next
	}
	want := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Liveness probes and CORS preflights are always exempt.
		if r.Method == http.MethodOptions || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Bearer realm="vibeforge"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
