package workflow

import (
	"context"
	"testing"

	"forge/internal/store"
)

// recordRunner records the inputs of every invocation so a test can assert exactly
// what got threaded into a re-run phase (answers vs feedback). It always succeeds.
type recordRunner struct{ inputs []map[string]any }

func (r *recordRunner) Run(_ context.Context, _ Step, inputs map[string]any, _ string) (StepResult, error) {
	r.inputs = append(r.inputs, inputs)
	return StepResult{Success: true, Output: map[string]any{"ok": true}}, nil
}

// answerWorkflow: a design-style phase (`prd`) gated by a human_gate whose on_fail
// loops back to the phase — the shape every design gate has.
const answerWorkflow = `
id: answerflow
version: 1.0.0
steps:
  - id: prd
    type: record
  - id: prd_gate
    type: human_gate
    on_fail:
      goto: prd
      max: 2
      feedback: $prd_gate.detail
`

// park drives the run one execute at a time until prd_gate is AWAITING.
func driveToGate(t *testing.T, e *Engine, runID string) {
	t.Helper()
	for i := 0; i < 4; i++ {
		if awaiting(e, runID, "prd_gate") {
			return
		}
		if _, err := e.ExecuteOne(context.Background(), "w"); err != nil {
			t.Fatalf("execute: %v", err)
		}
	}
	if !awaiting(e, runID, "prd_gate") {
		t.Fatalf("prd_gate never reached AWAITING")
	}
}

func awaiting(e *Engine, runID, stepID string) bool {
	tasks, _ := e.Store.TasksForRun(runID)
	for _, tk := range tasks {
		if tk.StepID == stepID && tk.Status == store.StatusAwaiting {
			return true
		}
	}
	return false
}

func countStep(e *Engine, runID, stepID string, status store.Status) int {
	tasks, _ := e.Store.TasksForRun(runID)
	n := 0
	for _, tk := range tasks {
		if tk.StepID == stepID && tk.Status == status {
			n++
		}
	}
	return n
}

// TestAnswerRerunsPhaseWithAnswersInput: answering a gate re-runs the target phase
// with the text as the `answers` input (NOT `feedback`), and the gate re-appears.
func TestAnswerRerunsPhaseWithAnswersInput(t *testing.T) {
	wf, _ := Parse([]byte(answerWorkflow))
	rec := &recordRunner{}
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("record", rec)
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("answerflow", nil)
	driveToGate(t, e, runID)
	if len(rec.inputs) != 1 {
		t.Fatalf("expected prd to run once before the gate, got %d", len(rec.inputs))
	}

	const answer = "use Firebase, not Postgres"
	if err := e.AnswerStep(runID, "prd_gate", answer); err != nil {
		t.Fatalf("AnswerStep: %v", err)
	}

	// prd re-runs (with answers), then the gate re-parks.
	driveToGate(t, e, runID)

	if len(rec.inputs) != 2 {
		t.Fatalf("expected prd to re-run after answer (2 runs), got %d", len(rec.inputs))
	}
	second := rec.inputs[1]
	if got := asString(second["answers"]); got != answer {
		t.Fatalf("re-run answers input = %q, want %q", got, answer)
	}
	if _, ok := second["feedback"]; ok {
		t.Fatalf("answer must NOT thread `feedback`, got %v", second["feedback"])
	}

	// The gate re-appears (a fresh AWAITING instance), the run did not auto-advance.
	if !awaiting(e, runID, "prd_gate") {
		t.Fatalf("gate did not re-appear as AWAITING after the phase re-ran")
	}
	// The answered gate resolved to DONE (not FAILED) so the failure budget is untouched.
	if f := countStep(e, runID, "prd_gate", store.StatusFailed); f != 0 {
		t.Fatalf("answer created %d FAILED gate task(s); it must create none", f)
	}
	if n, _ := e.Store.CountAnswers(runID, "prd_gate"); n != 1 {
		t.Fatalf("CountAnswers = %d, want 1", n)
	}
}

// TestAnswerDoesNotConsumeOnFailBudget: after several answers, the on_fail.max reject
// budget is still full — the run tolerates exactly max+1 reject attempts before failing.
// If answers consumed the budget, the run would fail earlier.
func TestAnswerDoesNotConsumeOnFailBudget(t *testing.T) {
	wf, _ := Parse([]byte(answerWorkflow))
	rec := &recordRunner{}
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("record", rec)
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("answerflow", nil)
	driveToGate(t, e, runID)

	// Three answers — none should count toward on_fail.max.
	for i := 0; i < 3; i++ {
		if err := e.AnswerStep(runID, "prd_gate", "answer round"); err != nil {
			t.Fatalf("answer %d: %v", i, err)
		}
		driveToGate(t, e, runID)
	}
	if f, _ := e.countFailures(runID, "prd_gate"); f != 0 {
		t.Fatalf("after 3 answers countFailures = %d, want 0 (answers must not count)", f)
	}

	// Now reject repeatedly. With max=2 the run loops on rejects 1 and 2 and only
	// FAILS on the 3rd (max+1) — proving the answers did not erode the budget.
	if err := e.RejectStep(runID, "prd_gate", "no"); err != nil {
		t.Fatalf("reject 1: %v", err)
	}
	driveToGate(t, e, runID)
	if s, _ := e.Store.GetRun(runID); s.Status == store.StatusFailed {
		t.Fatalf("run failed after reject 1 — the answers wrongly consumed the budget")
	}
	if err := e.RejectStep(runID, "prd_gate", "no"); err != nil {
		t.Fatalf("reject 2: %v", err)
	}
	driveToGate(t, e, runID)
	if s, _ := e.Store.GetRun(runID); s.Status == store.StatusFailed {
		t.Fatalf("run failed after reject 2 — budget should allow max+1=3 attempts")
	}
}

// TestAnswerCapEnforced: the hard anti-loop cap refuses the (maxAnswersPerPhase+1)th
// answer, and never resolves the gate on that refused call.
func TestAnswerCapEnforced(t *testing.T) {
	wf, _ := Parse([]byte(answerWorkflow))
	rec := &recordRunner{}
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("record", rec)
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("answerflow", nil)
	driveToGate(t, e, runID)

	for i := 0; i < maxAnswersPerPhase; i++ {
		if err := e.AnswerStep(runID, "prd_gate", "q"); err != nil {
			t.Fatalf("answer %d within cap: %v", i, err)
		}
		driveToGate(t, e, runID)
	}
	// The gate is AWAITING again; the next answer must be refused by the cap.
	if err := e.AnswerStep(runID, "prd_gate", "one too many"); err == nil {
		t.Fatalf("expected the %dth answer to be rejected by the cap", maxAnswersPerPhase+1)
	}
	// And the refused answer must not have resolved the gate.
	if !awaiting(e, runID, "prd_gate") {
		t.Fatalf("a capped answer must leave the gate AWAITING")
	}
	if n, _ := e.Store.CountAnswers(runID, "prd_gate"); n != maxAnswersPerPhase {
		t.Fatalf("CountAnswers = %d, want %d (the capped one not recorded)", n, maxAnswersPerPhase)
	}
}

// TestAnswerNoAwaitingIsTyped: answering when nothing is awaiting is the typed no-op,
// mirroring approve/reject.
func TestAnswerNoAwaitingIsTyped(t *testing.T) {
	wf, _ := Parse([]byte(answerWorkflow))
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("record", &recordRunner{})
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("answerflow", nil)
	// prd_gate is not yet awaiting (run just started).
	if err := e.AnswerStep(runID, "prd_gate", "x"); err != ErrNoAwaitingStep {
		t.Fatalf("answer with nothing awaiting = %v, want ErrNoAwaitingStep", err)
	}
}
