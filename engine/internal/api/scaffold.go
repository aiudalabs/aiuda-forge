package api

// POST /projects/{id}/scaffold/github — bake the GitHub-native specialization
// into the project's repo (F2 pivot): .github/agents per lane, AGENTS.md,
// CLAUDE.md, instructions, copilot-setup-steps and the QA workflows, rendered
// from registry/templates/github-native/<stack>. Idempotent: WriteFile skips
// identical content, so re-scaffolding after a template update only touches
// what changed.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"

	"forge/internal/projects"
	"forge/internal/scaffold"
)

// scaffoldTemplatesDir resolves the templates root (env override for deploys
// where the registry lives elsewhere; the Docker image ships it alongside).
func scaffoldTemplatesDir() string {
	if v := os.Getenv("VIBEFORGE_TEMPLATES_DIR"); v != "" {
		return v
	}
	return "registry/templates/github-native"
}

func (s *Server) scaffoldGitHub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "scaffolding requires editor or owner")
		return
	}
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.Repo == "" {
		httpErr(w, http.StatusBadRequest, "project has no repo")
		return
	}
	var req struct {
		Stack string `json:"stack"`
		Vars  map[string]string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Stack == "" {
		httpErr(w, http.StatusBadRequest, "stack is required (e.g. python-fastapi-react)")
		return
	}

	// Variables: defaults derivables del proyecto + lanes reales del backlog;
	// req.Vars (opcional) pisa cualquiera.
	vars := scaffold.Vars{
		"project_name": p.Name,
		"stack":        req.Stack,
		"language":     "es",
		// art_director gates the ui-verify visual-acceptance step (default on). A
		// project can turn it off (req.Vars below) to measure its cost/value. The
		// template's `if` reads `!= 'off'`, so on/absent both keep it on.
		"art_director": "on",
		// app_path: la app que buildean/verifican ui-verify + build-apk. SIN default,
		// el token {{app_path}} quedaba sin sustituir en los workflows y su guard
		// hashFiles nunca matcheaba → el build se saltaba en verde-vacío. Default
		// apps/customer (multi-app: el caller lo pisa vía req.Vars, y el matrix es fase 2).
		"app_path": "apps/customer",
	}
	if s.Tickets != nil {
		if stories, err := s.Tickets.ListStoriesByProject(id); err == nil {
			seen := map[string]bool{}
			var lanes []string
			for _, st := range stories {
				if st.Owner != "" && !seen[st.Owner] {
					seen[st.Owner] = true
					lanes = append(lanes, st.Owner)
				}
			}
			sort.Strings(lanes)
			vars["lanes"] = strings.Join(lanes, ", ")
		}
	}
	for k, v := range req.Vars {
		vars[k] = v
	}

	files, missing, err := scaffold.Render(scaffoldTemplatesDir(), req.Stack, vars)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "render: "+err.Error())
		return
	}
	written, skipped, err := scaffold.Apply(r.Context(), s.ghFor(r.Context(), id), p.Repo, "main", files, "chore(scaffold)")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "scaffold apply failed: " + err.Error(),
			"partial": map[string]int{"written": written, "skipped": skipped},
		})
		return
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"written": written,
		"skipped": skipped,
		"files":   paths,
		"missing": missing, // variables sin valor — quedaron {{tal_cual}} en el repo
	})
}
