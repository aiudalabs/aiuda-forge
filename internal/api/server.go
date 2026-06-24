// Package api is the control plane: the HTTP API that fulfils the contract in
// docs/14_API_CONTRACT_v2.md (§A operations, §B events, §C internal) plus the
// event bus. GOLDEN RULE: every operation goes through here and every state
// transition emits an event (from the store, the single emit point). There is no
// privileged path — UI, Brain and CLI are equal clients.
package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"

	"vibeforge-kernel/internal/settings"
	"vibeforge-kernel/internal/store"
	"vibeforge-kernel/internal/workflow"
)

// Server wires the store, the executor engine, the event bus, the registry, and
// the settings store into one HTTP handler.
type Server struct {
	Store    *store.Store
	Engine   *workflow.Engine
	Bus      *Bus
	Registry *Registry
	Settings *settings.Store
	mux      *http.ServeMux
}

// NewServer builds and routes a Server. The settings store lives next to the
// registry (registry/../settings.json) — config in the control plane, not the kernel.
func NewServer(st *store.Store, eng *workflow.Engine, bus *Bus, reg *Registry) *Server {
	set, _ := settings.Open(filepath.Join(filepath.Dir(reg.Root), "settings.json"))
	s := &Server{Store: st, Engine: eng, Bus: bus, Registry: reg, Settings: set, mux: http.NewServeMux()}
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
	// registry CRUD (compose/edit/list/delete agents, skills, workflows — no-code).
	// Generic by {kind}: workflows|agents|skills. PUT/POST validate against the
	// SAME parser the kernel uses, so a saved manifest is always runnable.
	m.HandleFunc("GET /registry/{kind}", s.listRegistry)
	m.HandleFunc("GET /registry/{kind}/{id}", s.getRegistry)
	m.HandleFunc("PUT /registry/{kind}/{id}", s.putRegistry)
	m.HandleFunc("POST /registry/{kind}/{id}", s.putRegistry)
	m.HandleFunc("DELETE /registry/{kind}/{id}", s.delRegistry)
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
	runs, err := s.Store.ListRuns(status)
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
	tasks, _ := s.Store.TasksForRun(id)
	if tasks == nil {
		tasks = []*store.Task{}
	}
	writeJSON(w, http.StatusOK, runView{Run: run, Steps: tasks})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.CancelRun(id); err != nil {
		notFound(w, err)
		return
	}
	run, _ := s.Store.GetRun(id)
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) deleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteRun(id); err != nil {
		notFound(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) retryRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Engine.RetryRun(id); err != nil {
		notFound(w, err)
		return
	}
	run, _ := s.Store.GetRun(id)
	writeJSON(w, http.StatusOK, run)
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
	// In the MVP the pr step already commits/pushes (PR_MODE=local); merge is an
	// explicit ack endpoint so the contract surface exists and is exercisable.
	writeJSON(w, http.StatusOK, map[string]any{"merged": step, "run": id})
}

func (s *Server) artifacts(w http.ResponseWriter, r *http.Request) {
	id, kind := r.PathValue("id"), r.PathValue("kind")
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
	runs, _ := s.Store.ListRuns("")
	byStatus := map[string]int{}
	costByWorkflow := map[string]float64{}
	costByStep := map[string]float64{}
	var totalCost float64
	done := 0
	for _, run := range runs {
		byStatus[string(run.Status)]++
		if run.Status == store.StatusDone {
			done++
		}
		// Cost lives in each step's Result (agent steps carry cost_usd). Aggregate
		// by workflow and by step. No kernel change — read what's already stored.
		tasks, _ := s.Store.TasksForRun(run.ID)
		for _, t := range tasks {
			c := costOf(t.Result)
			if c == 0 {
				continue
			}
			totalCost += c
			costByWorkflow[run.WorkflowID] += c
			costByStep[t.StepID] += c
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
	})
}

// costOf extracts cost_usd from a step's Result JSON (0 if absent/unparseable).
func costOf(result string) float64 {
	if result == "" {
		return 0
	}
	var m map[string]any
	if json.Unmarshal([]byte(result), &m) != nil {
		return 0
	}
	if c, ok := m["cost_usd"].(float64); ok {
		return c
	}
	return 0
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
	path, ok := s.Registry.pathFor(kind, r.PathValue("id"))
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.putRegistryFile(w, r, path, func(b []byte) error { return validateRegistry(kind, b) })
}

func (s *Server) delRegistry(w http.ResponseWriter, r *http.Request) {
	path, ok := s.Registry.pathFor(r.PathValue("kind"), r.PathValue("id"))
	if !ok {
		httpErr(w, http.StatusNotFound, "unknown registry kind")
		return
	}
	s.deleteRegistryFile(w, path)
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
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- §B events --------------------------------------------------------------

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
