package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"vibeforge-kernel/internal/agent"
	"vibeforge-kernel/internal/store"
	"vibeforge-kernel/internal/workflow"

	"gopkg.in/yaml.v3"
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

// SkillPath returns the on-disk path for a skill (markdown: the "how").
func (r *Registry) SkillPath(id string) string {
	return filepath.Join(r.Root, "skills", id+".md")
}

// pathFor returns the manifest path for a kind+id, or false for unknown kinds.
func (r *Registry) pathFor(kind, id string) (string, bool) {
	switch kind {
	case "workflows":
		return r.WorkflowPath(id), true
	case "agents":
		return r.AgentPath(id), true
	case "skills":
		return r.SkillPath(id), true
	}
	return "", false
}

// list returns the ids in a kind directory (filename without extension), sorted.
func (r *Registry) list(kind string) ([]string, error) {
	dir := filepath.Join(r.Root, kind)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if i := strings.LastIndex(n, "."); i > 0 {
			ids = append(ids, n[:i])
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// validateRegistry validates a manifest body for a kind. The schema check is the
// SAME parser the kernel uses to run it — so "saved" means "the kernel can run it".
func validateRegistry(kind string, body []byte) error {
	switch kind {
	case "workflows":
		_, err := workflow.Parse(body)
		return err
	case "agents":
		var m agent.Manifest
		if err := yaml.Unmarshal(body, &m); err != nil {
			return errors.New("invalid agent manifest: " + err.Error())
		}
		if m.Model == "" {
			return errors.New("agent manifest: model is required")
		}
		return nil
	case "skills":
		if len(strings.TrimSpace(string(body))) == 0 {
			return errors.New("skill content is empty")
		}
		return nil
	}
	return errors.New("unknown registry kind: " + kind)
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

func (s *Server) putRegistryFile(w http.ResponseWriter, r *http.Request, path string, validate func([]byte) error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	// Validate against the SAME parser the kernel uses → no invalid manifest lands.
	if validate != nil {
		if err := validate(buf); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid manifest: "+err.Error())
			return
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

func (s *Server) deleteRegistryFile(w http.ResponseWriter, path string) {
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			httpErr(w, http.StatusNotFound, "not found")
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Best-effort: drop the agent's persona sidecar too.
	if strings.HasSuffix(path, ".yaml") {
		_ = os.Remove(strings.TrimSuffix(path, ".yaml") + ".md")
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": filepath.Base(path)})
}
