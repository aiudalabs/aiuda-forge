package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"vibeforge-kernel/internal/store"
)

// Loader resolves a workflow id to its parsed manifest.
type Loader interface {
	Load(id string) (*Workflow, error)
}

// Engine is the GENERIC executor. Given a workflow + trigger it enqueues the
// first step; on each step completion it resolves the next step's inputs and
// enqueues it, or applies on_fail{goto,max}. It dispatches step execution by
// Type via the runner registry. There is deliberately no `if step.id == ...`.
type Engine struct {
	Store       *store.Store
	Loader      Loader
	WorkdirRoot string
	runners     map[string]Runner
}

// NewEngine builds an engine. workdirRoot is where per-run working trees live;
// if empty a temp dir is used.
func NewEngine(st *store.Store, loader Loader, workdirRoot string) *Engine {
	if workdirRoot == "" {
		workdirRoot, _ = os.MkdirTemp("", "vibeforge-runs-")
	}
	return &Engine{Store: st, Loader: loader, WorkdirRoot: workdirRoot, runners: map[string]Runner{}}
}

// Register wires a step-runner for a step type. Built-ins: echo, gate. Later
// waves register agent, agentic_verify, pr, human_gate the same way.
func (e *Engine) Register(stepType string, r Runner) { e.runners[stepType] = r }

// Workdir returns the stable working directory for a run.
func (e *Engine) Workdir(runID string) string { return filepath.Join(e.WorkdirRoot, runID) }

// StartRun creates a run for workflowID with the given trigger payload and
// enqueues its first step. Returns the run id.
func (e *Engine) StartRun(workflowID string, trigger map[string]any) (string, error) {
	wf, err := e.Loader.Load(workflowID)
	if err != nil {
		return "", err
	}
	if trigger == nil {
		trigger = map[string]any{}
	}
	payload, _ := json.Marshal(trigger)
	runID := newID("run")
	if _, err := e.Store.CreateRun(runID, workflowID, string(payload)); err != nil {
		return "", err
	}
	if err := os.MkdirAll(e.Workdir(runID), 0o755); err != nil {
		return "", err
	}
	ctx := Context{"trigger": trigger}
	first := wf.First()
	if err := e.enqueueStep(runID, workflowID, first, ctx); err != nil {
		return "", err
	}
	if err := e.Store.SetRunStatus(runID, store.StatusRunning); err != nil {
		return "", err
	}
	return runID, nil
}

// enqueueStep resolves a step's inputs against ctx and enqueues it as a task.
func (e *Engine) enqueueStep(runID, workflowID string, step Step, ctx Context) error {
	inputs := ResolveInputs(step.Inputs, ctx)
	payload, _ := json.Marshal(inputs)
	return e.Store.EnqueueTask(&store.Task{
		ID:         newID("task"),
		RunID:      runID,
		WorkflowID: workflowID,
		StepID:     step.ID,
		Type:       step.Type,
		Payload:    string(payload),
	})
}

// ExecuteOne claims one ready task, runs its step, reports the result, and
// advances the workflow. Returns (false, nil) when no task is ready.
func (e *Engine) ExecuteOne(ctx context.Context, workerID string) (bool, error) {
	task, err := e.Store.Claim(workerID)
	if err != nil {
		return false, err
	}
	if task == nil {
		return false, nil
	}
	wf, err := e.Loader.Load(task.WorkflowID)
	if err != nil {
		return true, e.failTask(task, "load workflow: "+err.Error())
	}
	step, ok := wf.StepByID(task.StepID)
	if !ok {
		return true, e.failTask(task, "step not in workflow: "+task.StepID)
	}
	runner, ok := e.runners[step.Type]
	if !ok {
		return true, e.failTask(task, "no runner for step type: "+step.Type)
	}

	var inputs map[string]any
	_ = json.Unmarshal([]byte(task.Payload), &inputs)

	result, runErr := runner.Run(ctx, step, inputs, e.Workdir(task.RunID))
	if runErr != nil {
		// Execution error (not a logical failure) — record and fail the step.
		return true, e.reportAndAdvance(wf, task, StepResult{Success: false, Detail: "runner error: " + runErr.Error()})
	}
	return true, e.reportAndAdvance(wf, task, result)
}

// reportAndAdvance records the step result (single emit point via the store
// transition) and then advances the workflow.
func (e *Engine) reportAndAdvance(wf *Workflow, task *store.Task, result StepResult) error {
	resMap := map[string]any{"success": result.Success, "output": result.Output, "detail": result.Detail}
	to := store.StatusDone
	errMsg := ""
	if !result.Success {
		to = store.StatusFailed
		errMsg = result.Detail
	}
	if err := e.Store.Transition(task.ID, task.Fence, to, resMap, errMsg); err != nil {
		return err
	}
	// step.gate / step.event style signals could be appended here in later waves.
	return e.advance(wf, task, result)
}

// advance is the heart of the executor: decide the next task to enqueue, loop
// back via on_fail, or terminate the run. Generic — driven by data only.
func (e *Engine) advance(wf *Workflow, task *store.Task, result StepResult) error {
	step, _ := wf.StepByID(task.StepID)
	ctx, err := e.buildContext(task.RunID)
	if err != nil {
		return err
	}

	if result.Success {
		next, ok := wf.Next(task.StepID)
		if !ok {
			return e.Store.SetRunStatus(task.RunID, store.StatusDone) // last step done
		}
		return e.enqueueStep(task.RunID, task.WorkflowID, next, ctx)
	}

	// Failure: apply on_fail{goto,max} if declared.
	if step.OnFail != nil && step.OnFail.Goto != "" {
		failures, err := e.countFailures(task.RunID, task.StepID)
		if err != nil {
			return err
		}
		if failures <= step.OnFail.Max {
			target, ok := wf.StepByID(step.OnFail.Goto)
			if !ok {
				return e.Store.SetRunStatus(task.RunID, store.StatusFailed)
			}
			// Inject feedback (resolved) into the goto target's inputs.
			if step.OnFail.Feedback != nil {
				fb := resolveValue(step.OnFail.Feedback, ctx)
				if target.Inputs == nil {
					target.Inputs = map[string]any{}
				}
				inputs := ResolveInputs(target.Inputs, ctx)
				inputs["feedback"] = asString(fb)
				payload, _ := json.Marshal(inputs)
				return e.Store.EnqueueTask(&store.Task{
					ID: newID("task"), RunID: task.RunID, WorkflowID: task.WorkflowID,
					StepID: target.ID, Type: target.Type, Payload: string(payload),
				})
			}
			return e.enqueueStep(task.RunID, task.WorkflowID, target, ctx)
		}
		// Cap exhausted.
		return e.Store.SetRunStatus(task.RunID, store.StatusFailed)
	}

	// No on_fail policy: the run fails.
	return e.Store.SetRunStatus(task.RunID, store.StatusFailed)
}

// buildContext gathers the trigger payload + every completed step's result for a
// run into the reference Context used to resolve inputs.
func (e *Engine) buildContext(runID string) (Context, error) {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	var trigger map[string]any
	_ = json.Unmarshal([]byte(run.Payload), &trigger)
	ctx := Context{"trigger": trigger}

	tasks, err := e.Store.TasksForRun(runID)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.Result == "" || t.Result == "{}" {
			continue
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(t.Result), &res); err != nil {
			continue
		}
		// Last writer for a step id wins (the most recent attempt).
		ctx[t.StepID] = res
	}
	return ctx, nil
}

// countFailures counts FAILED tasks for a step id in a run (drives on_fail.max).
func (e *Engine) countFailures(runID, stepID string) (int, error) {
	tasks, err := e.Store.TasksForRun(runID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tasks {
		if t.StepID == stepID && t.Status == store.StatusFailed {
			n++
		}
	}
	return n, nil
}

// failTask records an infrastructure failure on a claimed task and fails the run.
func (e *Engine) failTask(task *store.Task, msg string) error {
	if err := e.Store.Transition(task.ID, task.Fence, store.StatusFailed, map[string]any{"success": false, "detail": msg}, msg); err != nil {
		return err
	}
	return e.Store.SetRunStatus(task.RunID, store.StatusFailed)
}

// RunToCompletion drives the engine in-process until the run reaches a terminal
// state. This is the test/CLI driver; in production cmd/worker calls ExecuteOne.
func (e *Engine) RunToCompletion(ctx context.Context, runID string) (store.Status, error) {
	for i := 0; i < 10000; i++ {
		run, err := e.Store.GetRun(runID)
		if err != nil {
			return "", err
		}
		if store.IsTerminal(run.Status) {
			return run.Status, nil
		}
		busy, err := e.ExecuteOne(ctx, "inproc")
		if err != nil {
			return "", err
		}
		if !busy {
			// Nothing ready but run not terminal: stuck (e.g. awaiting approval).
			return run.Status, fmt.Errorf("no ready task but run %s not terminal (status %s)", runID, run.Status)
		}
	}
	return "", fmt.Errorf("run %s did not terminate within step budget", runID)
}
