package api

// Endpoints de los templates github-native (la especialización repo-baked).

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)


// ── Templates github-native (la especialización que el scaffold hornea) ────
// Read-write sobre registry/templates/github-native/**: la fuente de verdad de
// los .agent.md / instructions / workflows que viajan a cada repo. Editar aquí
// afecta a los PRÓXIMOS scaffolds (los repos existentes se actualizan
// re-scaffoldeando, que es idempotente).

// GET /registry/templates — lista recursiva de archivos de template.
func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	root := filepath.Join(s.Registry.Root, "templates", "github-native")
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// templatePath valida y resuelve el ?path= contra el root de templates
// (sin escapes fuera del árbol).
func (s *Server) templatePath(raw string) (string, bool) {
	root := filepath.Join(s.Registry.Root, "templates", "github-native")
	clean := filepath.Clean("/" + raw) // fuerza raíz, mata ".."
	p := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return p, true
}

// GET /registry/templates/file?path=… — contenido de un template.
func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.templatePath(r.URL.Query().Get("path"))
	if !ok {
		httpErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		httpErr(w, http.StatusNotFound, "template not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": r.URL.Query().Get("path"), "content": string(b)})
}

// PUT /registry/templates/file?path=… — guarda un template (texto plano; sin
// validación YAML porque los .tmpl llevan {{vars}} que no parsean).
func (s *Server) putTemplate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.templatePath(r.URL.Query().Get("path"))
	if !ok {
		httpErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if _, err := os.Stat(p); err != nil {
		httpErr(w, http.StatusNotFound, "template not found (solo edición; crear archivos nuevos requiere el repo)")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == "" {
		httpErr(w, http.StatusBadRequest, "content is required")
		return
	}
	if err := os.WriteFile(p, []byte(req.Content), 0o644); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}
