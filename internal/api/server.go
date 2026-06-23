// Package api is the control plane: the HTTP API that fulfils the contract in
// docs/14_API_CONTRACT_v2.md (§A operations, §B events, §C internal) plus the
// event bus. GOLDEN RULE: every operation goes through here and every state
// transition emits an event (from the store, the single emit point). There is no
// privileged path — UI, Brain and CLI are equal clients.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"vibeforge-kernel/internal/store"
	"vibeforge-kernel/internal/workflow"
)

// Server wires the store, the executor engine, the event bus, and the registry
// into one HTTP handler.
type Server struct {
	Store    *store.Store
	Engine   *workflow.Engine
	Bus      *Bus
	Registry *Registry
	mux      *http.ServeMux
}

// NewServer builds and routes a Server.
func NewServer(st *store.Store, eng *workflow.Engine, bus *Bus, reg *Registry) *Server {
	s := &Server{Store: st, Engine: eng, Bus: bus, Registry: reg, mux: http.NewServeMux()}
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
	m.HandleFunc("POST /control/pause", s.pause)
	m.HandleFunc("POST /control/resume", s.resume)
	m.HandleFunc("POST /runs/{id}/steps/{step}/approve", s.approveStep)
	m.HandleFunc("POST /runs/{id}/steps/{step}/merge", s.mergeStep)
	m.HandleFunc("GET /runs/{id}/artifacts/{kind}", s.artifacts)
	m.HandleFunc("GET /metrics", s.metrics)
	m.HandleFunc("GET /analytics", s.metrics)
	m.HandleFunc("GET /healthz", s.health)
	m.HandleFunc("GET /readyz", s.health)
	// registry CRUD (compose/edit agents, skills, workflows)
	m.HandleFunc("GET /registry/workflows/{id}", s.getWorkflow)
	m.HandleFunc("PUT /registry/workflows/{id}", s.putWorkflow)
	m.HandleFunc("GET /registry/agents/{id}", s.getAgent)
	m.HandleFunc("PUT /registry/agents/{id}", s.putAgent)

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
	for _, run := range runs {
		byStatus[string(run.Status)]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runs_total": len(runs),
		"by_status":  byStatus,
		"paused":     s.Engine.IsPaused(),
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- registry CRUD ----------------------------------------------------------

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	s.getRegistryFile(w, s.Registry.WorkflowPath(r.PathValue("id")))
}
func (s *Server) putWorkflow(w http.ResponseWriter, r *http.Request) {
	s.putRegistryFile(w, r, s.Registry.WorkflowPath(r.PathValue("id")))
}
func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	s.getRegistryFile(w, s.Registry.AgentPath(r.PathValue("id")))
}
func (s *Server) putAgent(w http.ResponseWriter, r *http.Request) {
	s.putRegistryFile(w, r, s.Registry.AgentPath(r.PathValue("id")))
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
