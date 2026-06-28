package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"forge/internal/github"
	"forge/internal/imports"
	"forge/internal/projects"
)

// importGitHub handles POST /projects/{id}/import/github: import the project repo's
// open GitHub issues into the backlog as stories (v1.3). Editor+ only (it writes
// the backlog). The repo defaults to the project's repo; an optional {"repo": …}
// body overrides it. Idempotent — issues already imported (by external_ref) are
// skipped.
func (s *Server) importGitHub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "importing requires editor or owner")
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
	// Optional body override for the repo; empty/absent body falls back to the
	// project's repo.
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
	res, err := imports.GitHub(r.Context(), s.Tickets, github.New(), id, repo)
	if err != nil {
		httpErr(w, http.StatusBadGateway, "github import failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
