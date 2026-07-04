package api

import (
	"errors"
	"net/http"

	"forge/internal/httpx"
	"forge/internal/tickets"
)

// depError maps tickets dep-graph validation errors (self-dep, cycle, missing
// dep) to a 400 and reports whether it handled the error (H6/H7).
func depError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, tickets.ErrDepCycle) || errors.Is(err, tickets.ErrDepNotFound) {
		httpErr(w, http.StatusBadRequest, err.Error())
		return true
	}
	return false
}

// ---- Epics ------------------------------------------------------------------

func (s *Server) createEpic(w http.ResponseWriter, r *http.Request) {
	var e tickets.Epic
	if !readJSON(w, r, &e) {
		return
	}
	if e.ID == "" {
		httpErr(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.Tickets.CreateEpic(e); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	got, err := s.Tickets.GetEpic(e.ID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, got)
}

func (s *Server) listEpics(w http.ResponseWriter, r *http.Request) {
	epics, err := s.Tickets.ListEpics()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if epics == nil {
		epics = []tickets.Epic{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"epics": epics})
}

func (s *Server) getEpic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, err := s.Tickets.GetEpic(id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// ---- Sprints ----------------------------------------------------------------

func (s *Server) createSprint(w http.ResponseWriter, r *http.Request) {
	var sp tickets.Sprint
	if !readJSON(w, r, &sp) {
		return
	}
	if sp.ID == "" {
		httpErr(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.Tickets.CreateSprint(sp); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

func (s *Server) listSprints(w http.ResponseWriter, r *http.Request) {
	sprints, err := s.Tickets.ListSprints()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sprints == nil {
		sprints = []tickets.Sprint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sprints": sprints})
}

// readySprints handles GET /sprints/ready — sprints that can be fired as a
// single goal-mode run (≥1 story, all backlog, external deps done).
func (s *Server) readySprints(w http.ResponseWriter, r *http.Request) {
	// ?project=<id> scopes ready sprints to one project (audit A1/A2) so the
	// scheduler evaluates each project's sprints under its own settings.
	project := r.URL.Query().Get("project")
	sprints, err := s.Tickets.ReadySprintsByProject(project)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sprints == nil {
		sprints = []tickets.Sprint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sprints": sprints})
}

// sprintStories handles GET /sprints/{id}/stories — the sprint's stories in
// intra-sprint topological order (the order the goal-mode ticket renders them).
func (s *Server) sprintStories(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	projectID := r.URL.Query().Get("project_id")
	stories, err := s.Tickets.StoriesBySprintScoped(id, projectID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stories == nil {
		stories = []tickets.Story{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stories": stories})
}

// claimSprint handles POST /sprints/{id}/claim. It atomically claims ALL the
// sprint's backlog stories at once (backlog→running). Returns 200
// {claimed:[ids...]} if this caller won, or 409 {claimed:[]} if a concurrent
// claimer already moved any of them (or the sprint is empty).
func (s *Server) claimSprint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	projectID := r.URL.Query().Get("project_id")
	claimed, ok, err := s.Tickets.ClaimSprint(id, projectID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"claimed": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claimed": claimed})
}

// updateSprintStatus handles PUT /sprints/{id}/status. It advances every running
// story in the sprint to done|failed (goal-mode completion) and, when a run_id is
// supplied, records it on all the sprint's stories so the UI can link them.
func (s *Server) updateSprintStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateStatusReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.RunID != "" {
		if err := s.Tickets.SetSprintRun(id, req.RunID); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	switch req.Status {
	case "": // run_id-only update (record the firing run on a still-running sprint)
	case tickets.StatusBacklog:
		// B3 sprint compensation: ResetSprintClaim sends status=backlog when a
		// FireRun failed after the sprint was claimed, so the sprint re-fires.
		if err := s.Tickets.MarkSprintBacklog(id); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case tickets.StatusInReview:
		if err := s.Tickets.MarkSprintInReview(id, req.PRURL); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case tickets.StatusDone:
		if err := s.Tickets.MarkSprintDone(id); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	case tickets.StatusFailed:
		if err := s.Tickets.MarkSprintFailed(id); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		httpErr(w, http.StatusBadRequest, "sprint status must be backlog, in_review, done or failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- Stories ----------------------------------------------------------------

func (s *Server) createStory(w http.ResponseWriter, r *http.Request) {
	var st tickets.Story
	if !readJSON(w, r, &st) {
		return
	}
	if st.ID == "" {
		httpErr(w, http.StatusBadRequest, "id is required")
		return
	}
	// Validate the repo URL at the API boundary (audit C5): a story's repo flows
	// into `gh pr merge` under auto-merge, so an unvalidated repo is RCE-adjacent.
	// Empty repo is allowed (local/echo flows); a non-empty one must pass.
	if st.Repo != "" {
		if err := httpx.ValidateRemote(st.Repo); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid repo: "+err.Error())
			return
		}
	}
	if err := s.Tickets.CreateStory(st); err != nil {
		if depError(w, err) {
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	got, err := s.Tickets.GetStory(st.ID)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, got)
}

func (s *Server) listStoriesHandler(w http.ResponseWriter, r *http.Request) {
	stories, err := s.Tickets.ListStories()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stories == nil {
		stories = []tickets.Story{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stories": stories})
}

func (s *Server) getStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := s.Tickets.GetStory(id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type updateStatusReq struct {
	Status tickets.Status `json:"status"`
	// RunID is optional: when present the story's run_id column is updated in
	// addition to the status. Used by the native scheduler to record which
	// control-plane run is executing a story (MarkRunning path).
	RunID string `json:"run_id,omitempty"`
	// PRURL is optional: when present (with status=in_review) the story's pr_url
	// column is recorded so the merge-reconcile loop can check the PR.
	PRURL string `json:"pr_url,omitempty"`
}

func (s *Server) updateStoryStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateStatusReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Status == "" {
		httpErr(w, http.StatusBadRequest, "status is required")
		return
	}
	// in_review records the PR URL alongside the status so the merge-reconcile
	// loop can find the PR; other statuses use the plain status update.
	if req.Status == tickets.StatusInReview {
		if err := s.Tickets.MarkInReview(id, req.PRURL); err != nil {
			ticketNotFound(w, err)
			return
		}
	} else if err := s.Tickets.UpdateStoryStatus(id, req.Status); err != nil {
		ticketNotFound(w, err)
		return
	}
	if req.RunID != "" {
		if err := s.Tickets.SetStoryRun(id, req.RunID); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	st, err := s.Tickets.GetStory(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type addDepsReq struct {
	Deps []string `json:"deps"`
}

func (s *Server) addStoryDeps(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Get story first to know its project scope
	existing, err := s.Tickets.GetStory(id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	var req addDepsReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Tickets.AddDep(id, existing.ProjectID, req.Deps); err != nil {
		if depError(w, err) {
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st, err := s.Tickets.GetStory(id)
	if err != nil {
		ticketNotFound(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// claimStory handles POST /stories/{id}/claim. It atomically transitions the
// story from backlog → running. Returns 200 {claimed:true} if this caller won
// the claim, or 409 {claimed:false} if it was already taken.
func (s *Server) claimStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	claimed, err := s.Tickets.ClaimStory(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !claimed {
		writeJSON(w, http.StatusConflict, map[string]any{"claimed": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claimed": true})
}

// ---- GET /tickets (compat) --------------------------------------------------

// ticketView is the shape the orchestrator's GET /tickets returns, so the
// existing UI can read the native store without changes.
type ticketView struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Body        string   `json:"body,omitempty"`       // the skeleton user-story ("As a X, I want Y…") so the board can show what a story is
	Acceptance  string   `json:"acceptance,omitempty"` // the falsifiable AC lines
	Status      string   `json:"status"`               // derived: backlog stories whose deps are done report "ready"
	Deps        []string `json:"deps"`
	RunID       string   `json:"run_id,omitempty"`
	SprintID    string   `json:"sprint_id,omitempty"`    // lets the scheduler group running stories by sprint
	EpicID      string   `json:"epic_id,omitempty"`      // parent epic — the board groups/labels by it
	Owner       string   `json:"owner,omitempty"`        // agent lane responsible — the board's "assignee"
	PRURL       string   `json:"pr_url,omitempty"`       // recorded in_review; the reconcile loop checks this PR
	SessionURL  string   `json:"session_url,omitempty"`  // the GitHub agent session executing it (F2 dispatch)
	ExternalRef string   `json:"external_ref,omitempty"` // GitHub mirror (github:owner/repo#N) — la UI decide requeue nativo vs legacy
	Repo        string   `json:"repo,omitempty"`         // the repo the PR lives in (needed to address it via gh)
	ProjectID   string   `json:"project_id,omitempty"`   // lets the scheduler group work by project (audit A1)
}

func (s *Server) ticketsCompat(w http.ResponseWriter, r *http.Request) {
	// ?project=<id> scopes the board to one project (audit A1); absent = all.
	project := r.URL.Query().Get("project")
	if s.crossTenantDenied(w, r.Context(), project, map[string]any{"tickets": []any{}}) {
		return // D1: a user session may only read its own project's board
	}
	stories, err := s.Tickets.ListStoriesByProject(project)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Build the ready set for O(1) lookup during the view construction. Ready is
	// not project-scoped (it's only a membership lookup) — the story list above is
	// already scoped, so a ready story outside the project is simply never matched.
	readyStories, err := s.Tickets.Ready()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	readySet := make(map[string]bool, len(readyStories))
	for _, st := range readyStories {
		readySet[st.ID] = true
	}

	// Sesiones de agente (F2 dispatch): un solo fetch en bloque para el view.
	sessions, err := s.Tickets.SessionURLs(project)
	if err != nil {
		sessions = map[string]string{} // best-effort: el board no se cae por esto
	}

	views := make([]ticketView, 0, len(stories))
	for _, st := range stories {
		status := string(st.Status)
		if st.Status == tickets.StatusBacklog && readySet[st.ID] {
			status = string(tickets.StatusReady)
		}
		deps := st.Deps
		if deps == nil {
			deps = []string{}
		}
		views = append(views, ticketView{
			ID:          st.ID,
			Title:       st.Title,
			Body:        st.Body,
			Acceptance:  st.Accept,
			Status:      status,
			Deps:        deps,
			RunID:       st.RunID,
			SprintID:    st.SprintID,
			EpicID:      st.EpicID,
			Owner:       st.Owner,
			PRURL:       st.PRURL,
			SessionURL:  sessions[st.ID],
			ExternalRef: st.ExternalRef,
			Repo:        st.Repo,
			ProjectID:   st.ProjectID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": views})
}

// ticketNotFound writes a 404 for ErrNotFound or a 500 for anything else.
func ticketNotFound(w http.ResponseWriter, err error) {
	if err == tickets.ErrNotFound {
		httpErr(w, http.StatusNotFound, err.Error())
		return
	}
	httpErr(w, http.StatusInternalServerError, err.Error())
}
