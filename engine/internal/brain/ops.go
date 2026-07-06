package brain

import (
	"fmt"

	"forge/internal/store"
	"forge/internal/tickets"
	"forge/internal/workflow"
)

// ControlOps is the set of control-plane operations the Brain's tools invoke,
// in-process. Defined as an interface so tests fake it; production is EngineOps.
type ControlOps interface {
	Status() (paused bool, pausedUntil int64)
	Pause()
	Resume()
	ListRuns(projectID string) ([]map[string]any, error)
	GetRun(id string) (map[string]any, error)
	CancelRun(id string) error
	RetryRun(id string) error
	RerunStep(runID, stepID string) error
	RequeueRun(id string) (int, error)
	StartRun(workflow string, payload map[string]any) (string, error)
	ApproveStep(runID, step string) error
	RejectStep(runID, step, reason string) error
	Metrics(projectID string) (map[string]any, error)
	ActiveState(projectID string) (map[string]any, error)

	// Registry / method authoring. The registry (workflows/agents/skills) IS the
	// methodology, as DATA. read+list are reversible; write+delete MUTATE that data
	// and are gated behind human approval. This is what turns the Brain from an
	// operator of flows into an author of the method.
	ListRegistry(kind string) ([]string, error)
	ReadRegistry(kind, id string) (string, error)
	WriteRegistry(kind, id, content string) error
	DeleteRegistry(kind, id string) error

	// Inspection / diagnosis. Read the document a run's step produced, and the run's
	// event timeline — so the Brain can review artifacts and diagnose failures the
	// way an operator does. Both reversible (read-only).
	Artifact(runID, stepID string) (string, error)
	RunEvents(runID string) ([]map[string]any, error)

	// Exec is the escape hatch: run a shell command on the control host. The most
	// powerful and most dangerous capability — gated hard (mutating, owner-only,
	// opt-in per deployment). It's what lets the Brain do the long tail (gh, git,
	// docker, scripts) that no specific tool covers.
	Exec(command string) (string, error)
}

// EngineOps is the production ControlOps, wired to the kernel Engine, store, and
// native ticket store. It mirrors what the HTTP handlers do, called in-process.
type EngineOps struct {
	Engine  *workflow.Engine
	Store   *store.Store
	Tickets *tickets.Store
	// RegistryDir is the root of the methodology registry (workflows/, agents/,
	// skills/). Wired from VIBEFORGE_REGISTRY so the Brain edits the SAME files the
	// kernel reads — no privileged path (kernel constitution).
	RegistryDir string
}

func (o EngineOps) Status() (bool, int64) {
	if o.Engine == nil {
		return false, 0
	}
	return o.Engine.IsPaused(), o.Engine.PausedUntil()
}
func (o EngineOps) Pause()  { o.Engine.Pause() }
func (o EngineOps) Resume() { o.Engine.Resume() }

func (o EngineOps) ListRuns(projectID string) ([]map[string]any, error) {
	runs, err := o.Store.ListRunsByProject("", projectID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		out = append(out, map[string]any{
			"id": r.ID, "workflow": r.WorkflowID, "status": string(r.Status), "created_at": r.CreatedAt,
		})
	}
	return out, nil
}

func (o EngineOps) GetRun(id string) (map[string]any, error) {
	r, err := o.Store.GetRun(id)
	if err != nil {
		return nil, err
	}
	tasks, _ := o.Store.TasksForRun(id)
	steps := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		step := map[string]any{"step": t.StepID, "type": t.Type, "status": string(t.Status)}
		if t.Error != "" {
			step["error"] = t.Error
		}
		// Surface a failed step's result so the Brain can diagnose without a second call.
		if string(t.Status) == "FAILED" && t.Result != "" && t.Result != "{}" {
			step["result"] = t.Result
		}
		steps = append(steps, step)
	}
	return map[string]any{
		"id": r.ID, "workflow": r.WorkflowID, "status": string(r.Status),
		"project_id": r.ProjectID, "steps": steps,
	}, nil
}

func (o EngineOps) CancelRun(id string) error { return o.Store.CancelRun(id) }
func (o EngineOps) RetryRun(id string) error  { return o.Engine.RetryRun(id) }

func (o EngineOps) RerunStep(runID, stepID string) error {
	return o.Engine.RerunStep(runID, stepID)
}

func (o EngineOps) RequeueRun(id string) (int, error) {
	if o.Tickets == nil {
		return 0, fmt.Errorf("ticket store not configured")
	}
	return o.Tickets.RequeueByRun(id)
}

func (o EngineOps) StartRun(wf string, payload map[string]any) (string, error) {
	return o.Engine.StartRun(wf, payload)
}

func (o EngineOps) ApproveStep(runID, step string) error { return o.Engine.ApproveStep(runID, step) }
func (o EngineOps) RejectStep(runID, step, reason string) error {
	return o.Engine.RejectStep(runID, step, reason)
}

// Metrics returns a compact status summary for a project (counts by run status).
// Cost/token detail lives in the Spend view; the Brain only needs the shape of
// progress to answer "resume el estado".
func (o EngineOps) Metrics(projectID string) (map[string]any, error) {
	runs, err := o.Store.ListRunsByProject("", projectID)
	if err != nil {
		return nil, err
	}
	byStatus := map[string]int{}
	for _, r := range runs {
		byStatus[string(r.Status)]++
	}
	return map[string]any{"runs_total": len(runs), "by_status": byStatus}, nil
}

// ActiveState returns a DIGESTED snapshot of the project's CURRENT state, so the
// Brain doesn't have to summarize the firehose of list_runs (which includes every
// old terminal run and invites a wrong narrative). It reports the engine pause flag,
// the ACTIVE runs (queued/running/awaiting), any steps awaiting human approval, and
// a count of terminal (done/failed/cancelled) runs — present but not enumerated.
func (o EngineOps) ActiveState(projectID string) (map[string]any, error) {
	runs, err := o.Store.ListRunsByProject("", projectID)
	if err != nil {
		return nil, err
	}
	active := []map[string]any{}
	awaiting := []map[string]any{}
	terminal := 0
	for _, r := range runs {
		switch r.Status {
		case store.StatusQueued, store.StatusRunning, store.StatusAwaiting:
			active = append(active, map[string]any{
				"id": r.ID, "workflow": r.WorkflowID, "status": string(r.Status),
			})
			// A run parks at a human gate by leaving the run RUNNING while one task
			// goes AWAITING — surface those so "what needs approval" is answerable.
			tasks, _ := o.Store.TasksForRun(r.ID)
			for _, t := range tasks {
				if t.Status == store.StatusAwaiting {
					awaiting = append(awaiting, map[string]any{"run_id": r.ID, "step": t.StepID})
				}
			}
		default:
			terminal++
		}
	}
	paused, until := o.Status()
	return map[string]any{
		"paused":            paused,
		"paused_until":      until,
		"active_runs":       active,
		"awaiting_approval": awaiting,
		"terminal_runs":     terminal,
	}, nil
}
