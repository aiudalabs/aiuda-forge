package api

// POST /projects/{id}/export/github — exports the project's backlog to GitHub as
// issues + native blocked_by dependencies (F0 of the GitHub-native pivot). Mirror
// of importGitHub. Synchronous: a 44-story backlog takes a couple of minutes (the
// exporter paces writes for rate limits); the console shows indeterminate progress.
// Idempotent — a mid-run failure returns 502 with partial counts and a retry
// continues where it left off (stories already carrying external_ref are skipped).

import (
	"encoding/json"
	"errors"
	"net/http"

	"forge/internal/export"
	"forge/internal/github"
	"forge/internal/projects"
)

func (s *Server) exportGitHub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "exporting requires editor or owner")
		return
	}
	if s.Tickets == nil {
		httpErr(w, http.StatusServiceUnavailable, "ticket store not configured")
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
	var req struct {
		Repo string `json:"repo"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // best-effort; empty body is fine
	repo := req.Repo
	if repo == "" {
		repo = p.Repo
	}
	if repo == "" {
		httpErr(w, http.StatusBadRequest, "project has no repo; pass {\"repo\": \"https://github.com/owner/name\"}")
		return
	}
	res, err := export.GitHubBacklog(r.Context(), s.Tickets, github.New(), id, repo)
	if err != nil {
		// Partial counts travel with the error so the console can say how far it got.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "github export failed: " + err.Error(),
			"partial": res,
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
