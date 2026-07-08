package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
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

// RerunStep re-runs a SINGLE step of an existing run IN PLACE: it reuses the run's
// workdir (its docs are already there — no re-clone) and does NOT cascade to the
// downstream steps. Use it to regenerate one phase after changing that phase's
// persona/model — e.g. re-run `mockups` after switching the designer to Opus —
// without re-running the whole design or re-publishing the backlog/PR to GitHub.
func (e *Engine) RerunStep(runID, stepID, feedback string) error {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return err
	}
	wf, err := e.Loader.Load(run.WorkflowID)
	if err != nil {
		return err
	}
	step, ok := wf.StepByID(stepID)
	if !ok {
		return fmt.Errorf("run %q has no step %q", runID, stepID)
	}
	if err := e.Store.ReopenRunForRerun(runID); err != nil {
		return err
	}
	ctx, err := e.buildContext(runID)
	if err != nil {
		return err
	}
	return e.enqueueStepRerun(runID, run.WorkflowID, step, ctx, feedback)
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

// maxAnswersPerPhase is the hard anti-loop cap on the `answer` verb. Unlike reject,
// an answer does NOT consume on_fail.max (it is not a rejection), so it needs its own
// ceiling so a pathological answer→re-run→answer loop cannot run forever. Humans answer
// a handful of open questions per phase; 50 is far above any real interaction.
const maxAnswersPerPhase = 50

// AnswerStep answers a human_gate's open questions WITHOUT rejecting it: the target
// phase (the gate's on_fail.goto) is re-run with the answer text injected as the
// `answers` input, and the gate re-appears for approval. Crucially it does NOT count
// against on_fail.max — the gate task is resolved to DONE (not FAILED), so countFailures
// (which only counts FAILED tasks) is untouched; the answer budget is tracked separately
// (maxAnswersPerPhase). No-op (ErrNoAwaitingStep) if nothing is awaiting.
func (e *Engine) AnswerStep(runID, stepID, text string) error {
	// Hard anti-loop cap (checked before resolving so the (N+1)th answer is refused).
	n, err := e.Store.CountAnswers(runID, stepID)
	if err != nil {
		return err
	}
	if n >= maxAnswersPerPhase {
		return fmt.Errorf("answer cap reached for step %q (%d answers)", stepID, maxAnswersPerPhase)
	}
	task, ok, err := e.Store.AnswerAwaiting(runID, stepID, text)
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
	return e.advanceAnswer(wf, task, text)
}

// advanceAnswer re-enqueues the gate's on_fail target with the answer as the `answers`
// input (parallel to how a reject re-enqueues it with `feedback`, but NOT counting as a
// failure and NOT capped by on_fail.max). The target phase regenerates the doc
// incorporating the answers, then its downstream gate re-parks for approval.
func (e *Engine) advanceAnswer(wf *Workflow, gateTask *store.Task, text string) error {
	step, ok := wf.StepByID(gateTask.StepID)
	if !ok {
		return fmt.Errorf("answer: step %q not in workflow", gateTask.StepID)
	}
	if step.OnFail == nil || step.OnFail.Goto == "" {
		return fmt.Errorf("answer: step %q has no on_fail.goto target to answer into", gateTask.StepID)
	}
	target, ok := wf.StepByID(step.OnFail.Goto)
	if !ok {
		return fmt.Errorf("answer: on_fail.goto %q is not a step", step.OnFail.Goto)
	}
	ctx, err := e.buildContext(gateTask.RunID)
	if err != nil {
		return err
	}
	inputs := ResolveInputs(target.Inputs, ctx)
	if inputs == nil {
		inputs = map[string]any{}
	}
	inputs["answers"] = text
	payload, _ := json.Marshal(inputs)
	return e.Store.EnqueueTask(&store.Task{
		ID: newID("task"), RunID: gateTask.RunID, WorkflowID: gateTask.WorkflowID,
		StepID: target.ID, Type: target.Type, Payload: string(payload),
	})
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
func (e *Engine) Resume() { e.paused.Store(false); e.pausedUntil.Store(0) }

// IsPaused reports the pause state.
func (e *Engine) IsPaused() bool { return e.paused.Load() }

// PausedUntil returns the auto-resume time (unix millis) if the pause was a credit
// circuit-breaker, or 0 for a manual/indefinite pause.
func (e *Engine) PausedUntil() int64 { return e.pausedUntil.Load() }

// PauseUntil is the credit circuit-breaker: a provider usage/session limit pauses
// the WHOLE factory (no worker claims new steps) until resumeAt, then auto-resumes.
// Re-arming while already paused keeps the LATER resume time. This stops the thrash
// of firing/retrying into an exhausted-credits wall.
func (e *Engine) PauseUntil(resumeAt int64, reason string) {
	if cur := e.pausedUntil.Load(); cur >= resumeAt && e.paused.Load() {
		return // already paused at least this long
	}
	e.pausedUntil.Store(resumeAt)
	if !e.paused.Swap(true) {
		log.Printf("engine: circuit-breaker PAUSED until %s — %s", time.UnixMilli(resumeAt).UTC().Format("15:04 MST"), reason)
	}
}

// paused is declared on Engine in engine.go via embedding atomic.Bool below.

// ---- credit circuit-breaker --------------------------------------------------

var sessionLimitRe = regexp.MustCompile(`(?i)session limit|usage limit|weekly limit|hit your .*limit`)
var resetTimeRe = regexp.MustCompile(`(?i)resets\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)`)

// creditLimit detects a provider usage/credit limit in a step's error OR result
// text (the limit shows up both ways: an execution error, and an agent that reports
// "You've hit your session limit" as its own verdict). It returns the auto-resume
// time (unix millis), parsed from "resets 4am (UTC)" so the factory waits exactly
// until the window opens; if unparseable it falls back to a 30-min backoff (the
// breaker re-arms on the next hit, so even a wrong guess self-corrects).
func creditLimit(errText, resultText string, now time.Time) (resumeAt int64, ok bool) {
	text := errText + "\n" + resultText
	if !sessionLimitRe.MatchString(text) {
		return 0, false
	}
	resumeAt = now.Add(30 * time.Minute).UnixMilli() // fallback
	if m := resetTimeRe.FindStringSubmatch(text); m != nil {
		h, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		if strings.EqualFold(m[3], "pm") && h != 12 {
			h += 12
		}
		if strings.EqualFold(m[3], "am") && h == 12 {
			h = 0
		}
		u := now.UTC()
		reset := time.Date(u.Year(), u.Month(), u.Day(), h, min, 0, 0, time.UTC)
		if !reset.After(u) {
			reset = reset.Add(24 * time.Hour) // next occurrence of that wall-clock time
		}
		resumeAt = reset.UnixMilli()
	}
	return resumeAt, true
}

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
			// Circuit-breaker auto-resume: once the credit window has elapsed, the
			// first worker to flip the flag logs and re-enables claims.
			if until := e.pausedUntil.Load(); until > 0 && time.Now().UnixMilli() >= until {
				if e.paused.Swap(false) {
					e.pausedUntil.Store(0)
					log.Printf("engine: circuit-breaker auto-resumed (credit window elapsed)")
				}
			} else {
				time.Sleep(250 * time.Millisecond)
				continue
			}
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
