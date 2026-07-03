package api

// Dispatch endpoints (F2 pivot): expose the conductor's ready-set and fire
// dispatches under the project's autonomy policy. approve-mode is the default:
// the console shows candidates, a human confirms each one here.

import (
	"encoding/json"
	"errors"
	"net/http"

	"forge/internal/conductor"
	"forge/internal/projects"
)

// policyFor adapts projects.Settings to the conductor's Policy.
func (s *Server) policyFor(projectID string) (conductor.Policy, error) {
	set, err := s.Projects.GetSettings(projectID)
	if err != nil {
		return conductor.Policy{}, err
	}
	return conductor.Policy{
		ExecutionUnit:  set.ExecutionUnit,
		DispatchMode:   set.DispatchMode,
		Executor:       set.Executor,
		ModelByLane:    set.ModelByLane,
		ExecutorByLane: set.ExecutorByLane,
		MaxConcurrency: set.MaxConcurrency,
	}, nil
}

// GET /projects/{id}/dispatch/candidates — what could be dispatched right now.
func (s *Server) dispatchCandidates(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleViewer) {
		httpErr(w, http.StatusForbidden, "listing candidates requires project membership")
		return
	}
	if s.Dispatcher == nil {
		httpErr(w, http.StatusServiceUnavailable, "dispatch not configured")
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
	pol, err := s.policyFor(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cands, err := s.Dispatcher.Candidates(r.Context(), id, p.Repo, pol)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cands == nil {
		cands = []conductor.Candidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"execution_unit": pol.ExecutionUnit,
		"candidates":     cands,
	})
}

// POST /projects/{id}/dispatch {story_id|sprint_id} — fire one candidate.
func (s *Server) dispatchWork(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "dispatching requires editor or owner")
		return
	}
	if s.Dispatcher == nil {
		httpErr(w, http.StatusServiceUnavailable, "dispatch not configured")
		return
	}
	var req struct {
		StoryID  string `json:"story_id"`
		SprintID string `json:"sprint_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	unit := req.StoryID
	if unit == "" {
		unit = req.SprintID
	}
	if unit == "" {
		httpErr(w, http.StatusBadRequest, "story_id or sprint_id is required")
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
	pol, err := s.policyFor(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pol.DispatchMode == projects.DispatchOff {
		httpErr(w, http.StatusConflict, "dispatch is off for this project")
		return
	}
	res, err := s.Dispatcher.Dispatch(r.Context(), id, p.Repo, pol, unit)
	if err != nil {
		if errors.Is(err, conductor.ErrNotCandidate) {
			httpErr(w, http.StatusConflict, err.Error())
			return
		}
		httpErr(w, http.StatusBadGateway, "dispatch failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
