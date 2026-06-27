// Package api is the control plane: the HTTP API that fulfils the contract in
// docs/14_API_CONTRACT_v2.md (§A operations, §B events, §C internal) plus the
// event bus. GOLDEN RULE: every operation goes through here and every state
// transition emits an event (from the store, the single emit point). There is no
// privileged path — UI, Brain and CLI are equal clients.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"forge/internal/auth"
	"forge/internal/httpx"
	"forge/internal/projects"
	"forge/internal/settings"
	"forge/internal/store"
	"forge/internal/tickets"
	"forge/internal/workflow"
)

// Server wires the store, the executor engine, the event bus, the registry,
// the settings store, the native ticket store, and the project store into one
// HTTP handler.
type Server struct {
	Store    *store.Store
	Engine   *workflow.Engine
	Bus      *Bus
	Registry *Registry
	Settings *settings.Store
	Tickets  *tickets.Store
	Projects *projects.Store // nil when ProjectsDB is not configured
	Auth     *auth.Store     // nil when AuthDB is not configured
	mux      *http.ServeMux
}

// NewServer builds and routes a Server. The settings store lives next to the
// registry (registry/../settings.json) — config in the control plane, not the kernel.
// tix and proj may be nil; their routes return 503 until they are set
// (needTickets / needProjects guards).
func NewServer(st *store.Store, eng *workflow.Engine, bus *Bus, reg *Registry, tix *tickets.Store, proj *projects.Store, au *auth.Store) *Server {
	set, _ := settings.Open(filepath.Join(filepath.Dir(reg.Root), "settings.json"))
	s := &Server{Store: st, Engine: eng, Bus: bus, Registry: reg, Settings: set, Tickets: tix, Projects: proj, Auth: au, mux: http.NewServeMux()}
	s.routes()
	return s
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	m := s.mux
	// §A — operations the UI uses.
	m.HandleFunc("POST /runs", s.createRun)
	m.HandleFunc("GET /runs", s.listRuns)
	m.HandleFunc("GET /runs/{id}", s.getRun)
	m.HandleFunc("POST /runs/{id}/cancel", s.cancelRun)
	m.HandleFunc("DELETE /runs/{id}", s.deleteRun)
	m.HandleFunc("POST /runs/{id}/retry", s.retryRun)
	// Requeue (R2): resurrect a failed run's stories back to backlog so the
	// orchestrator re-fires. In sprint mode this requeues the WHOLE sprint.
	m.HandleFunc("POST /runs/{id}/requeue", s.needTickets(s.requeueRun))
	m.HandleFunc("GET /control/status", s.controlStatus)
	m.HandleFunc("POST /control/pause", s.pause)
	m.HandleFunc("POST /control/resume", s.resume)
	m.HandleFunc("POST /runs/{id}/steps/{step}/approve", s.approveStep)
	m.HandleFunc("POST /runs/{id}/steps/{step}/reject", s.rejectStep)
	m.HandleFunc("POST /runs/{id}/steps/{step}/merge", s.mergeStep)
	m.HandleFunc("GET /runs/{id}/artifacts/{kind}", s.artifacts)
	m.HandleFunc("GET /metrics", s.metrics)
	m.HandleFunc("GET /analytics", s.metrics)
	m.HandleFunc("GET /healthz", s.health)
	m.HandleFunc("GET /readyz", s.health)
	// Local auth (email+password). POST /auth/login is the one unauthenticated
	// endpoint (exempted in the httpx.Auth middleware); logout/me require a token.
	m.HandleFunc("POST /auth/login", s.needAuth(s.login))
	m.HandleFunc("POST /auth/logout", s.needAuth(s.logout))
	m.HandleFunc("GET /auth/me", s.needAuth(s.me))
	// registry CRUD (compose/edit/list/delete agents, skills, workflows — no-code).
	// Generic by {kind}: workflows|agents|skills. PUT/POST validate against the
	// SAME parser the kernel uses, so a saved manifest is always runnable.
	m.HandleFunc("GET /registry/{kind}", s.listRegistry)
	m.HandleFunc("GET /registry/{kind}/{id}", s.getRegistry)
	m.HandleFunc("PUT /registry/{kind}/{id}", s.putRegistry)
	m.HandleFunc("POST /registry/{kind}/{id}", s.putRegistry)
	m.HandleFunc("DELETE /registry/{kind}/{id}", s.delRegistry)
	// Persona sidecar for agents: the <id>.md file next to the <id>.yaml manifest.
	m.HandleFunc("GET /registry/agents/{id}/persona", s.getAgentPersona)
	// settings (MCP connections, agent auth, sandbox, merge policy) — secrets masked.
	m.HandleFunc("GET /settings", s.getSettings)
	m.HandleFunc("PUT /settings", s.putSettings)

	// §B — events.
	m.HandleFunc("GET /runs/{id}/events", s.events)
	m.HandleFunc("GET /ws", s.websocket)

	// §C — daemon-internal worker↔kernel.
	m.HandleFunc("POST /runs/claim", s.claim)
	m.HandleFunc("POST /steps/{id}/report", s.report)
	m.HandleFunc("POST /steps/{id}/heartbeat", s.heartbeat)
	m.HandleFunc("POST /steps/{id}/usage", s.usage)

	// Project store. Guarded per-request — Projects may be nil (no ProjectsDB configured).
	m.HandleFunc("POST /projects", s.needProjects(s.createProject))
	m.HandleFunc("GET /projects", s.needProjects(s.listProjects))
	// Per-project settings (audit A2). Registered before "/projects/{id}" patterns
	// are not needed — Go's ServeMux matches the more specific pattern first.
	m.HandleFunc("GET /projects/{id}/settings", s.needProjects(s.getProjectSettings))
	m.HandleFunc("PUT /projects/{id}/settings", s.needProjects(s.putProjectSettings))
	m.HandleFunc("GET /projects/{id}", s.needProjects(s.getProject))
	// Studio = Confluence (U1): the project's specs read straight from its repo
	// docs/ (source of truth that outlives an ephemeral design run).
	m.HandleFunc("GET /projects/{id}/docs", s.needProjects(s.listProjectDocs))
	m.HandleFunc("GET /projects/{id}/docs/file", s.needProjects(s.getProjectDoc))

	// Native ticket store. Registered unconditionally and guarded per-request:
	// Tickets may be nil (no TicketsDB configured) — needTickets returns 503 in that case.
	m.HandleFunc("POST /epics", s.needTickets(s.createEpic))
	m.HandleFunc("GET /epics", s.needTickets(s.listEpics))
	m.HandleFunc("GET /epics/{id}", s.needTickets(s.getEpic))
	m.HandleFunc("POST /sprints", s.needTickets(s.createSprint))
	m.HandleFunc("GET /sprints", s.needTickets(s.listSprints))
	// Sprint-batched (goal-mode) endpoints the orchestrator polls. "/sprints/ready"
	// is a more specific pattern than "/sprints/{id}/..." so it routes correctly.
	m.HandleFunc("GET /sprints/ready", s.needTickets(s.readySprints))
	m.HandleFunc("GET /sprints/{id}/stories", s.needTickets(s.sprintStories))
	m.HandleFunc("POST /sprints/{id}/claim", s.needTickets(s.claimSprint))
	m.HandleFunc("PUT /sprints/{id}/status", s.needTickets(s.updateSprintStatus))
	m.HandleFunc("POST /sprints/{id}/requeue", s.needTickets(s.requeueSprint))
	m.HandleFunc("POST /stories", s.needTickets(s.createStory))
	m.HandleFunc("GET /stories", s.needTickets(s.listStoriesHandler))
	m.HandleFunc("GET /stories/{id}", s.needTickets(s.getStory))
	m.HandleFunc("PUT /stories/{id}/status", s.needTickets(s.updateStoryStatus))
	m.HandleFunc("POST /stories/{id}/claim", s.needTickets(s.claimStory))
	m.HandleFunc("POST /stories/{id}/deps", s.needTickets(s.addStoryDeps))
	// GET /tickets — compat endpoint matching the orchestrator's shape so the
	// existing UI can read the native store unchanged.
	m.HandleFunc("GET /tickets", s.needTickets(s.ticketsCompat))
}

// needTickets guards a handler that requires the native ticket store, returning
// 503 if it has not been wired in.
func (s *Server) needTickets(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Tickets == nil {
			httpErr(w, http.StatusServiceUnavailable, "ticket store not configured")
			return
		}
		h(w, r)
	}
}

// ---- §A handlers ------------------------------------------------------------

type createRunReq struct {
	Workflow string         `json:"workflow"`
	Payload  map[string]any `json:"payload"`
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var req createRunReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Workflow == "" {
		httpErr(w, http.StatusBadRequest, "workflow is required")
		return
	}
	runID, err := s.Engine.StartRun(req.Workflow, req.Payload)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	run, _ := s.Store.GetRun(runID)
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	status := store.Status(r.URL.Query().Get("status"))
	// ?project=<id> scopes the list to one project (audit A1); absent = all
	// projects (back-compat / service-token admin).
	project := r.URL.Query().Get("project")
	// Multi-tenant (D1): a user session may only list runs of a project it owns —
	// no/unowned project yields an empty list (no cross-tenant leak). The service
	// token (orchestrator) is unrestricted.
	if httpx.UserIDFromContext(r.Context()) != "" {
		if project == "" || !s.canAccessProject(r.Context(), project) {
			writeJSON(w, http.StatusOK, map[string]any{"runs": []*store.Run{}})
			return
		}
	}
	runs, err := s.Store.ListRunsByProject(status, project)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []*store.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// runView is a run plus its steps (tasks) — what GET /runs/{id} returns.
type runView struct {
	*store.Run
	Steps []*store.Task `json:"steps"`
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.Store.GetRun(id)
	if err != nil {
		notFound(w, err)
		return
	}
	if !s.canAccessProject(r.Context(), run.ProjectID) {
		httpErr(w, http.StatusNotFound, "run not found: "+id) // 404, not 403 (no existence leak)
		return
	}
	if run.DeletedAt != 0 {
		// Soft-deleted (D4): gone to clients, but the row + tasks + events are
		// retained in the DB for audit and so dependents never dangle.
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	tasks, _ := s.Store.TasksForRun(id)
	if tasks == nil {
		tasks = []*store.Task{}
	}
	writeJSON(w, http.StatusOK, runView{Run: run, Steps: tasks})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Store.CancelRun(id); err != nil {
		notFound(w, err)
		return
	}
	run, _ := s.Store.GetRun(id)
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) deleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Store.DeleteRun(id); err != nil {
		notFound(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) retryRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Engine.RetryRun(id); err != nil {
		notFound(w, err)
		return
	}
	run, _ := s.Store.GetRun(id)
	writeJSON(w, http.StatusOK, run)
}

// requeueRun (R2) resurrects a failed run's stories back to backlog so the
// orchestrator re-fires them. Because a goal-mode sprint's stories share the
// run_id, this requeues the WHOLE sprint; in story mode it requeues the one story.
func (s *Server) requeueRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	n, err := s.Tickets.RequeueByRun(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requeued": n})
}

// requeueSprint (R2) resurrects all of a sprint's failed stories back to backlog.
func (s *Server) requeueSprint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := s.Tickets.RequeueSprint(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requeued": n})
}

func (s *Server) controlStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"paused": s.Engine.IsPaused()})
}

func (s *Server) pause(w http.ResponseWriter, r *http.Request) {
	s.Engine.Pause()
	writeJSON(w, http.StatusOK, map[string]any{"paused": true})
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	s.Engine.Resume()
	writeJSON(w, http.StatusOK, map[string]any{"paused": false})
}

func (s *Server) approveStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	if err := s.Engine.ApproveStep(id, step); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approved": step, "run": id})
}

type rejectReq struct {
	Reason string `json:"reason"`
}

func (s *Server) rejectStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	var req rejectReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Engine.RejectStep(id, step, req.Reason); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rejected": step, "run": id, "reason": req.Reason})
}

func (s *Server) mergeStep(w http.ResponseWriter, r *http.Request) {
	id, step := r.PathValue("id"), r.PathValue("step")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	// In the MVP the pr step already commits/pushes (PR_MODE=local); merge is an
	// explicit ack endpoint so the contract surface exists and is exercisable.
	writeJSON(w, http.StatusOK, map[string]any{"merged": step, "run": id})
}

func (s *Server) artifacts(w http.ResponseWriter, r *http.Request) {
	id, kind := r.PathValue("id"), r.PathValue("kind")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	tasks, err := s.Store.TasksForRun(id)
	if err != nil {
		notFound(w, err)
		return
	}
	for _, t := range tasks {
		if t.StepID == kind {
			var result map[string]any
			_ = json.Unmarshal([]byte(t.Result), &result)
			writeJSON(w, http.StatusOK, map[string]any{"run": id, "kind": kind, "result": result})
			return
		}
	}
	httpErr(w, http.StatusNotFound, "no artifact for step "+kind)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	// ?project=<id> scopes the metrics to one project (absent = all projects).
	project := r.URL.Query().Get("project")
	if s.crossTenantDenied(w, r.Context(), project, map[string]any{
		"runs_total": 0, "by_status": map[string]int{}, "total_cost_usd": 0,
		"cost_by_workflow": map[string]float64{}, "cost_by_step": map[string]float64{}, "acceptance_rate": 0,
	}) {
		return // D1: a user session may only read its own project's spend
	}
	runs, _ := s.Store.ListRunsByProject("", project)
	byStatus := map[string]int{}
	costByWorkflow := map[string]float64{}
	costByStep := map[string]float64{}
	var totalCost float64
	var tokensIn, tokensOut, turns, agentCalls int
	done := 0
	for _, run := range runs {
		byStatus[string(run.Status)]++
		if run.Status == store.StatusDone {
			done++
		}
		// Usage lives in each agent step's Result.output (cost_usd / tokens / turns).
		// Aggregate by workflow and by step. No kernel change — read what's stored.
		tasks, _ := s.Store.TasksForRun(run.ID)
		for _, t := range tasks {
			u := usageOf(t.Result)
			if u.cost == 0 && u.tokensIn == 0 && u.tokensOut == 0 {
				continue
			}
			agentCalls++
			totalCost += u.cost
			tokensIn += u.tokensIn
			tokensOut += u.tokensOut
			turns += u.turns
			costByWorkflow[run.WorkflowID] += u.cost
			costByStep[t.StepID] += u.cost
		}
	}
	acceptance := 0.0
	if len(runs) > 0 {
		acceptance = float64(done) / float64(len(runs))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runs_total":       len(runs),
		"by_status":        byStatus,
		"paused":           s.Engine.IsPaused(),
		"total_cost_usd":   totalCost,
		"cost_by_workflow": costByWorkflow,
		"cost_by_step":     costByStep,
		"acceptance_rate":  acceptance,
		"total_tokens_in":  tokensIn,
		"total_tokens_out": tokensOut,
		"total_turns":      turns,
		"agent_calls":      agentCalls,
	})
}

// stepUsage is the per-step agent usage read from a stored Result.
type stepUsage struct {
	cost              float64
	tokensIn, tokensOut, turns int
}

// usageOf extracts agent usage from a step's Result JSON. The agent runner stores
// these under Result.output (cost_usd / tokens_in / tokens_out / num_turns); a
// top-level fallback keeps older rows working.
func usageOf(result string) stepUsage {
	if result == "" {
		return stepUsage{}
	}
	var m map[string]any
	if json.Unmarshal([]byte(result), &m) != nil {
		return stepUsage{}
	}
	src := m
	if out, ok := m["output"].(map[string]any); ok {
		src = out
	}
	f := func(k string) float64 {
		if v, ok := src[k].(float64); ok {
			return v
		}
		return 0
	}
	return stepUsage{
		cost:      f("cost_usd"),
		tokensIn:  int(f("tokens_in")),
		tokensOut: int(f("tokens_out")),
		turns:     int(f("num_turns")),
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- registry CRUD ----------------------------------------------------------

func (s *Server) listRegistry(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if _, ok := s.Registry.pathFor(kind, "_"); !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind: "+kind)
		return
	}
	ids, err := s.Registry.list(kind)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": kind, "ids": ids})
}

func (s *Server) getRegistry(w http.ResponseWriter, r *http.Request) {
	path, ok := s.Registry.pathFor(r.PathValue("kind"), r.PathValue("id"))
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.getRegistryFile(w, path)
}

func (s *Server) putRegistry(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id := r.PathValue("id")
	path, ok := s.Registry.pathFor(kind, id)
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.putRegistryFile(w, r, path, func(b []byte) error { return validateRegistry(kind, b) })
	// Invalidate the workflow cache so the next run re-reads the new manifest.
	if kind == "workflows" && s.Engine != nil {
		s.Engine.InvalidateWorkflow(id)
	}
}

func (s *Server) delRegistry(w http.ResponseWriter, r *http.Request) {
	path, ok := s.Registry.pathFor(r.PathValue("kind"), r.PathValue("id"))
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.deleteRegistryFile(w, path)
}

// getAgentPersona serves the markdown persona sidecar (<id>.md) for an agent.
// Returns 200 with the file body (text/plain) or 404 if the sidecar is absent.
// Uses the same path-traversal guard as pathFor so id can never escape the registry.
func (s *Server) getAgentPersona(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Resolve and validate via pathFor (agents kind) to get the traversal-safe
	// path to the .yaml manifest, then swap the extension.
	yamlPath, ok := s.Registry.pathFor("agents", id)
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown agent id")
		return
	}
	mdPath := yamlPath[:len(yamlPath)-len(".yaml")] + ".md"
	b, err := os.ReadFile(mdPath)
	if err != nil {
		httpErr(w, http.StatusNotFound, "persona not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// ---- settings ---------------------------------------------------------------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Settings.Get()) // secrets masked
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settings.Settings
	if !readJSON(w, r, &in) {
		return
	}
	out, err := s.Settings.Put(in)
	if err != nil {
		// A rejected execution_unit (or any validation failure) is a client error,
		// not a server fault — surface it as 400 so the UI can show the reason.
		if errors.Is(err, settings.ErrInvalid) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- §B events --------------------------------------------------------------

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runAccessible(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "run not found: "+id)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	evs, err := s.Store.EventsAfter(id, after)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if evs == nil {
		evs = []*store.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": evs})
}

// ---- §C internal ------------------------------------------------------------

type claimReq struct {
	Worker string `json:"worker"`
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	var req claimReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Worker == "" {
		req.Worker = "worker"
	}
	task, err := s.Store.Claim(req.Worker)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

type reportReq struct {
	Fence   int64          `json:"fence"`
	Success bool           `json:"success"`
	Output  map[string]any `json:"output"`
	Detail  string         `json:"detail"`
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req reportReq
	if !readJSON(w, r, &req) {
		return
	}
	err := s.Engine.ReportStep(id, req.Fence, workflow.StepResult{Success: req.Success, Output: req.Output, Detail: req.Detail})
	if err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reported": id})
}

type heartbeatReq struct {
	Fence int64 `json:"fence"`
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req heartbeatReq
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Store.Heartbeat(id, req.Fence); err != nil {
		httpErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	task, err := s.Store.GetTask(id)
	if err != nil {
		notFound(w, err)
		return
	}
	_, _ = s.Store.AppendEvent(task.RunID, id, "step.usage", body)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
