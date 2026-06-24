package orchestrator

import (
	"encoding/json"
	"net/http"
)

// HTTPServer is the small HTTP server the orchestrator exposes for the UI.
// It serves a single endpoint: GET /tickets.
type HTTPServer struct {
	orch *Orchestrator
	mux  *http.ServeMux
}

// NewHTTPServer builds an HTTPServer backed by orch.
func NewHTTPServer(orch *Orchestrator) *HTTPServer {
	s := &HTTPServer{orch: orch, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /tickets", s.tickets)
	s.mux.HandleFunc("GET /healthz", s.health)
	return s
}

// ServeHTTP makes HTTPServer an http.Handler.
func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

type ticketsResp struct {
	Tickets []Ticket `json:"tickets"`
}

func (s *HTTPServer) tickets(w http.ResponseWriter, r *http.Request) {
	tickets, err := s.orch.Tickets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ticketsResp{Tickets: tickets})
}

func (s *HTTPServer) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// writeJSON is a package-local helper matching the pattern in internal/api.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
