package api

// Dispatch endpoints (F2 pivot): expose the conductor's ready-set and fire
// dispatches under the project's autonomy policy. approve-mode is the default:
// the console shows candidates, a human confirms each one here.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
		// Sin capacidad (p.ej. claude_action sin el secret): 409 con el motivo visible
		// — la UI lo muestra y el usuario NO lanza un run que muere al final (Bug B).
		if errors.Is(err, conductor.ErrNoCapacity) {
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
	gh := s.ghFor(r.Context(), id)
	out := make([]executorView, 0, 2)
	okCop, why := gh.AgentTasksAvailable(r.Context(), p.Repo)
	if !okCop && strings.Contains(why, "does not have read access") {
		// La Agent tasks API no acepta (aún) el token user-to-server de la App
		// (permiso Copilot pendiente en el manifest). Degradación: en dev el
		// host puede; en cloud queda la razón honesta.
		if ok2, _ := github.New().AgentTasksAvailable(r.Context(), p.Repo); ok2 {
			okCop, why = true, ""
		}
	}
	out = append(out, executorView{ID: projects.ExecutorCopilot, Available: okCop, Reason: why, Default: set.Executor == projects.ExecutorCopilot})
	okCl, clErr := gh.FileOnBranch(r.Context(), p.Repo, "main", ".github/workflows/claude.yml")
	clWhy := ""
	if clErr != nil {
		clWhy = clErr.Error()
	} else if !okCl {
		clWhy = "el repo no tiene .github/workflows/claude.yml en main (scaffold pendiente)"
	} else {
		// El workflow existe pero sin el secret la sesión muere en el arranque:
		// el probe debe decir la verdad completa (hallazgo de la simulación).
		if hasSecret, serr := gh.RepoSecretExists(r.Context(), p.Repo, "CLAUDE_CODE_OAUTH_TOKEN"); serr == nil && !hasSecret {
			okCl = false
			clWhy = "falta el secret CLAUDE_CODE_OAUTH_TOKEN en el repo — añádelo en Settings → Canal Claude"
		}
	}
	out = append(out, executorView{ID: projects.ExecutorClaudeAction, Available: okCl, Reason: clWhy, Default: set.Executor == projects.ExecutorClaudeAction})
	writeJSON(w, http.StatusOK, map[string]any{"executors": out})
}

// PUT /projects/{id}/secrets/claude {token} — siembra el secret que el canal
// claude_action necesita en el repo del proyecto (gh lo cifra con la public
// key del repo). El token NUNCA se persiste en Forja: pasa directo a GitHub.
func (s *Server) setClaudeSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "setting secrets requires editor or owner")
		return
	}
	p, err := s.Projects.Get(id)
	if err != nil || p.Repo == "" {
		httpErr(w, http.StatusNotFound, "project or repo not found")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Token) == "" {
		httpErr(w, http.StatusBadRequest, "token is required")
		return
	}
	if err := s.ghFor(r.Context(), id).SetRepoSecret(r.Context(), p.Repo, "CLAUDE_CODE_OAUTH_TOKEN", strings.TrimSpace(req.Token)); err != nil {
		httpErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}
