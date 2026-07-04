package api

// Dispatch endpoints (F2 pivot): expose the conductor's ready-set and fire
// dispatches under the project's autonomy policy. approve-mode is the default:
// the console shows candidates, a human confirms each one here.

import (
	"encoding/json"
	"errors"
	"net/http"

	"forge/internal/conductor"
	"forge/internal/github"
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
		// Executor opcional: override del canal SOLO para este despacho.
		Executor string `json:"executor"`
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
	if req.Executor != "" {
		if req.Executor != projects.ExecutorCopilot && req.Executor != projects.ExecutorClaudeAction {
			httpErr(w, http.StatusBadRequest, "invalid executor: "+req.Executor)
			return
		}
		// Override del canal SOLO para este despacho; el ruteo por lane cede.
		pol.Executor = req.Executor
		pol.ExecutorByLane = nil
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

// GET /projects/{id}/executors — qué canales están REALMENTE disponibles en
// el GitHub del proyecto: copilot (probe de la Agent tasks API) y
// claude_action (¿existe claude.yml en main?). La UI arma el selector con esto.
func (s *Server) listExecutors(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleViewer) {
		httpErr(w, http.StatusForbidden, "listing executors requires project membership")
		return
	}
	p, err := s.Projects.Get(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	set, _ := s.Projects.GetSettings(id)
	type executorView struct {
		ID        string `json:"id"`
		Available bool   `json:"available"`
		Reason    string `json:"reason,omitempty"`
		Default   bool   `json:"default"`
	}
	gh := github.New()
	out := make([]executorView, 0, 2)
	okCop, why := gh.AgentTasksAvailable(r.Context(), p.Repo)
	out = append(out, executorView{ID: projects.ExecutorCopilot, Available: okCop, Reason: why, Default: set.Executor == projects.ExecutorCopilot})
	okCl, clErr := gh.FileOnBranch(r.Context(), p.Repo, "main", ".github/workflows/claude.yml")
	clWhy := ""
	if clErr != nil {
		clWhy = clErr.Error()
	} else if !okCl {
		clWhy = "el repo no tiene .github/workflows/claude.yml en main (scaffold pendiente)"
	}
	out = append(out, executorView{ID: projects.ExecutorClaudeAction, Available: okCl, Reason: clWhy, Default: set.Executor == projects.ExecutorClaudeAction})
	writeJSON(w, http.StatusOK, map[string]any{"executors": out})
}
