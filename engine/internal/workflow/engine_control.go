package workflow

import (
	"context"
	"errors"
	"time"

	"forge/internal/store"
)

// ErrNoAwaitingStep is returned by ApproveStep/RejectStep when no task for the
// (run, step) is AWAITING — already resolved, cancelled, or the loser of a
// concurrent approve/reject. A typed result so the API can report a clear 409
// instead of a misleading 200-success on a silent no-op (M3).
var ErrNoAwaitingStep = errors.New("no step awaiting approval")

// ReportStep records a worker's result for a claimed task (enforcing its fence)
// and advances the workflow. This is what the API's POST /steps/{id}/report
// calls — the daemon-internal worker→kernel path.
func (e *Engine) ReportStep(taskID string, fence int64, result StepResult) error {
	task, err := e.Store.GetTask(taskID)
	if err != nil {
		return err
	}
	task.Fence = fence
	wf, err := e.Loader.Load(task.WorkflowID)
	if err != nil {
		return err
	}
	return e.reportAndAdvance(wf, task, result)
}

// RetryRun reopens a terminal run and re-enqueues its last failed step. Driven
// by POST /runs/{id}/retry.
func (e *Engine) RetryRun(runID string) error {
	if err := e.Store.ReopenRun(runID); err != nil {
		return err
	}
	failed, err := e.Store.LastFailedTask(runID)
	if err != nil {
		return err
	}
	if failed == nil {
		return nil
	}
	wf, err := e.Loader.Load(failed.WorkflowID)
	if err != nil {
		return err
	}
	step, ok := wf.StepByID(failed.StepID)
	if !ok {
		return nil
	}
	ctx, err := e.buildContext(runID)
	if err != nil {
		return err
	}
	return e.enqueueStep(runID, failed.WorkflowID, step, ctx)
}

// ApproveStep resolves a human_gate that is awaiting approval for stepID in
// runID: it completes the paused gate task and advances the flow. If no step is
// awaiting approval it is a no-op (idempotent). Full human_gate semantics land
// in Wave 6; the operation and its endpoint exist now.
func (e *Engine) ApproveStep(runID, stepID string) error {
	return e.resolveAwaiting(runID, stepID, store.StatusDone, StepResult{
		Success: true, Output: map[string]any{"approved": true}, Detail: "approved by human",
	}, "")
}

// RejectStep rejects a human_gate that is awaiting approval: it completes the
// parked gate as a FAILURE carrying the reason, so advance() applies the step's
// on_fail (e.g. goto implement with feedback=$<gate>.detail → a fix round) or, if
// the workflow declares none, the run fails. Behavior is in DATA (the workflow),
// not here. The reason is recorded for audit. No-op if nothing is awaiting.
func (e *Engine) RejectStep(runID, stepID, reason string) error {
	return e.resolveAwaiting(runID, stepID, store.StatusFailed, StepResult{
		Success: false, Output: map[string]any{"rejected": true, "reason": reason}, Detail: reason,
	}, reason)
}

// resolveAwaiting atomically transitions the AWAITING (run, step) task to `to`
// (DONE for approve, FAILED for reject) via the store, then advances the flow —
// but only for the WINNER. The store does the find+transition under one write
// lock (M3), so a concurrent approve/reject or a racing cancel can never double-
// transition or double-advance: the loser gets ok=false → ErrNoAwaitingStep.
// The result is carried on the transitioned task so advance() resolves the next
// step's inputs (or on_fail feedback for a reject) from it.
func (e *Engine) resolveAwaiting(runID, stepID string, to store.Status, result StepResult, errMsg string) error {
	resMap := map[string]any{"success": result.Success, "output": result.Output, "detail": result.Detail}
	task, ok, err := e.Store.ResolveAwaiting(runID, stepID, to, resMap, errMsg)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoAwaitingStep
	}
	wf, err := e.Loader.Load(task.WorkflowID)
	if err != nil {
		return err
	}
	// The store already transitioned + emitted; advance only (no second transition).
	return e.advance(wf, task, result)
}

// ---- pause / resume ----------------------------------------------------------

// Pause halts new claims (the factory pause from POST /control/pause).
func (e *Engine) Pause() { e.paused.Store(true) }

// Resume re-enables claims.
func (e *Engine) Resume() { e.paused.Store(false) }

// IsPaused reports the pause state.
func (e *Engine) IsPaused() bool { return e.paused.Load() }

// paused is declared on Engine in engine.go via embedding atomic.Bool below.

// ---- background loops --------------------------------------------------------

// WorkerLoop claims and executes ready tasks until ctx is done, honoring pause
// and backing off when idle. This is the in-process worker; cmd/worker runs the
// equivalent over HTTP.
func (e *Engine) WorkerLoop(ctx context.Context, workerID string) {
	idle := 5 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if e.IsPaused() {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		busy, err := e.ExecuteOne(ctx, workerID)
		if err != nil || !busy {
			time.Sleep(idle)
		}
	}
}

// ReaperLoop periodically requeues stale (heartbeat-expired) tasks.
func (e *Engine) ReaperLoop(ctx context.Context, staleMillis int64, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = e.Store.RequeueStale(staleMillis)
		}
	}
}
