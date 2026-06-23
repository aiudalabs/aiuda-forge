package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"vibeforge-kernel/internal/store"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func notFound(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		httpErr(w, http.StatusNotFound, err.Error())
		return
	}
	httpErr(w, http.StatusInternalServerError, err.Error())
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

// Registry exposes the registry directory for CRUD over agents/workflows. It is
// how the API satisfies "compose/edit agents, skills, workflows" from the
// contract without a privileged path.
type Registry struct {
	Root string // registry/
}

// NewRegistry roots a registry at dir.
func NewRegistry(dir string) *Registry { return &Registry{Root: dir} }

// WorkflowPath returns the on-disk path for a workflow manifest.
func (r *Registry) WorkflowPath(id string) string {
	return filepath.Join(r.Root, "workflows", id+".yaml")
}

// AgentPath returns the on-disk path for an agent manifest.
func (r *Registry) AgentPath(id string) string {
	return filepath.Join(r.Root, "agents", id+".yaml")
}

func (s *Server) getRegistryFile(w http.ResponseWriter, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *Server) putRegistryFile(w http.ResponseWriter, r *http.Request, path string) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": filepath.Base(path)})
}
