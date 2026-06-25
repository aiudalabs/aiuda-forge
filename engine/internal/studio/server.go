package studio

import (
	"encoding/json"
	"net/http"
)

// HTTPServer exposes the Studio API over HTTP.
// Routes follow the same method-prefixed pattern as internal/api/server.go.
type HTTPServer struct {
	studio *Studio
	mux    *http.ServeMux
}

// NewHTTPServer builds an HTTPServer backed by s.
func NewHTTPServer(s *Studio) *HTTPServer {
	srv := &HTTPServer{studio: s, mux: http.NewServeMux()}
	srv.routes()
	return srv
}

// ServeHTTP makes HTTPServer an http.Handler.
func (h *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *HTTPServer) routes() {
	m := h.mux
	// Projects
	m.HandleFunc("POST /projects", h.createProject)
	m.HandleFunc("GET /projects", h.listProjects)
	m.HandleFunc("GET /projects/{id}", h.getProject)

	// Phase lifecycle
	m.HandleFunc("POST /projects/{id}/phases/{phase}/run", h.runPhase)
	m.HandleFunc("POST /projects/{id}/phases/{phase}/approve", h.approvePhase)
	m.HandleFunc("POST /projects/{id}/phases/{phase}/reject", h.rejectPhase)

	// Artifacts
	m.HandleFunc("GET /projects/{id}/artifacts", h.listArtifacts)
	m.HandleFunc("GET /projects/{id}/artifacts/{name}", h.getArtifact)

	// Handoff
	m.HandleFunc("POST /projects/{id}/handoff", h.handoff)

	// Health
	m.HandleFunc("GET /healthz", h.health)
}

// ---- handlers ---------------------------------------------------------------

type createProjectReq struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *HTTPServer) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.ID == "" || req.Name == "" {
		httpErr(w, http.StatusBadRequest, "id and name are required")
		return
	}
	proj, err := h.studio.CreateProject(req.ID, req.Name)
	if err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, proj)
}

func (h *HTTPServer) listProjects(w http.ResponseWriter, r *http.Request) {
	projects := h.studio.ListProjects()
	if projects == nil {
		projects = []*Project{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (h *HTTPServer) getProject(w http.ResponseWriter, r *http.Request) {
	proj, err := h.studio.GetProject(r.PathValue("id"))
	if err != nil {
		notFound(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proj)
}

func (h *HTTPServer) runPhase(w http.ResponseWriter, r *http.Request) {
	id, phase := r.PathValue("id"), r.PathValue("phase")
	if err := h.studio.RunPhase(r.Context(), id, phase); err != nil {
		if isNotFound(err) {
			notFound(w, err)
			return
		}
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	proj, _ := h.studio.GetProject(id)
	writeJSON(w, http.StatusOK, proj)
}

func (h *HTTPServer) approvePhase(w http.ResponseWriter, r *http.Request) {
	id, phase := r.PathValue("id"), r.PathValue("phase")
	if err := h.studio.ApprovePhase(id, phase); err != nil {
		if isNotFound(err) {
			notFound(w, err)
			return
		}
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	proj, _ := h.studio.GetProject(id)
	writeJSON(w, http.StatusOK, proj)
}

type rejectPhaseReq struct {
	Feedback string `json:"feedback"`
}

func (h *HTTPServer) rejectPhase(w http.ResponseWriter, r *http.Request) {
	id, phase := r.PathValue("id"), r.PathValue("phase")
	var req rejectPhaseReq
	_ = readJSON(w, r, &req) // feedback is optional
	if err := h.studio.RejectPhase(id, phase, req.Feedback); err != nil {
		if isNotFound(err) {
			notFound(w, err)
			return
		}
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	proj, _ := h.studio.GetProject(id)
	writeJSON(w, http.StatusOK, proj)
}

func (h *HTTPServer) listArtifacts(w http.ResponseWriter, r *http.Request) {
	arts, err := h.studio.ListArtifacts(r.PathValue("id"))
	if err != nil {
		if isNotFound(err) {
			notFound(w, err)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if arts == nil {
		arts = []Artifact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": arts})
}

func (h *HTTPServer) getArtifact(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("name")
	art, content, err := h.studio.GetArtifact(id, name)
	if err != nil {
		if isNotFound(err) {
			notFound(w, err)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact": art,
		"content":  content,
	})
}

type handoffReq struct {
	Items []BacklogItem `json:"items"`
}

func (h *HTTPServer) handoff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req handoffReq
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Items) == 0 {
		httpErr(w, http.StatusBadRequest, "items is required")
		return
	}
	issued, err := h.studio.Handoff(r.Context(), id, req.Items)
	if err != nil {
		if isNotFound(err) {
			notFound(w, err)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": id,
		"issued":  issued,
	})
}

func (h *HTTPServer) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- helpers -----------------------------------------------------------------

// writeJSON is a package-local helper matching the pattern in internal/api.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func notFound(w http.ResponseWriter, err error) {
	httpErr(w, http.StatusNotFound, err.Error())
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return contains([]string{err.Error()}, "not found") || containsStr(err.Error(), "not found")
}

// containsStr checks if a string contains a substring.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || findStr(s, sub))
}

func findStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// readJSON decodes the request body into v, writing a 400 on failure. Returns
// false if the caller should stop.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && err.Error() != "EOF" {
		httpErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return false
	}
	return true
}
