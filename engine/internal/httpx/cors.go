// Package httpx holds small HTTP middlewares shared by the control-plane,
// orchestrator and studio servers.
package httpx

import "net/http"

// CORS wraps next with permissive-but-configurable CORS headers so a browser
// frontend served from a different origin (e.g. the Next.js app on :3000) can
// call the API on :8080/:9090/:9091. origin is the value for
// Access-Control-Allow-Origin ("*" allows any; set a specific origin to lock it
// down). Preflight OPTIONS requests are answered here and never reach next.
func CORS(origin string, next http.Handler) http.Handler {
	if origin == "" {
		origin = "*"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
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
