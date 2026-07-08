package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/store"
)

func newEngine(t *testing.T, loader Loader) *Engine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wf.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	e := NewEngine(st, loader, t.TempDir())
	e.Register("echo", EchoRunner{})
	e.Register("gate", GateRunner{})
	return e
}

// slowRunner blocks for d before succeeding — stands in for a long agent call.
type slowRunner struct{ d time.Duration }

func (s slowRunner) Run(ctx context.Context, _ Step, _ map[string]any, _ string) (StepResult, error) {
	select {
	case <-time.After(s.d):
	case <-ctx.Done():
	}
	return StepResult{Success: true, Output: map[string]any{"slept": true}}, nil
}

// parkRunner stands in for a human_gate: it parks (AWAITING) so a test can
// drive ApproveStep/RejectStep against a real parked task.
type parkRunner struct{}

func (parkRunner) Run(_ context.Context, _ Step, _ map[string]any, _ string) (StepResult, error) {
	return StepResult{Park: true, Detail: "awaiting human"}, nil
}

// TestConcurrentApproveResolvesOnce (M3): a parked human_gate approved by two
// callers concurrently must resolve exactly once — one wins, the other gets the
// typed ErrNoAwaitingStep (not a 500/illegal-transition, not a misleading
// success), and the gate's next step is enqueued exactly once.
func TestConcurrentApproveResolvesOnce(t *testing.T) {
	src := []byte(`
id: gateflow
version: 1.0.0
steps:
  - id: review
    type: human_gate
  - id: ship
    type: echo
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"gateflow": wf})
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("gateflow", nil)
	// Drive the review step until it parks (AWAITING).
	if _, err := e.ExecuteOne(context.Background(), "w"); err != nil {
		t.Fatalf("execute review: %v", err)
	}
	tasks, _ := e.Store.TasksForRun(runID)
	if len(tasks) != 1 || tasks[0].Status != store.StatusAwaiting {
		t.Fatalf("expected review AWAITING, got %+v", tasks)
	}

	// Two concurrent approves: exactly one succeeds, the other is ErrNoAwaitingStep.
	type res struct{ err error }
	ch := make(chan res, 2)
	for i := 0; i < 2; i++ {
		go func() { ch <- res{e.ApproveStep(runID, "review")} }()
	}
	var wins, noAwait int
	for i := 0; i < 2; i++ {
		r := <-ch
		switch {
		case r.err == nil:
			wins++
		case errors.Is(r.err, ErrNoAwaitingStep):
			noAwait++
		default:
			t.Fatalf("unexpected approve error: %v", r.err)
		}
	}
	if wins != 1 || noAwait != 1 {
		t.Fatalf("expected exactly one winner + one no-awaiting, got wins=%d noAwait=%d", wins, noAwait)
	}

	// The ship step was enqueued exactly once (no double-advance).
	tasks, _ = e.Store.TasksForRun(runID)
	ship := 0
	for _, tk := range tasks {
		if tk.StepID == "ship" {
			ship++
		}
	}
	if ship != 1 {
		t.Fatalf("expected ship enqueued exactly once, got %d", ship)
	}
}

// TestRejectRacingCancelIsTyped (M3): rejecting a gate that was already cancelled
// returns ErrNoAwaitingStep, not a misleading nil success.
func TestRejectRacingCancelIsTyped(t *testing.T) {
	src := []byte(`
id: gatecancel
version: 1.0.0
steps:
  - id: review
    type: human_gate
  - id: ship
    type: echo
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"gatecancel": wf})
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("gatecancel", nil)
	if _, err := e.ExecuteOne(context.Background(), "w"); err != nil {
		t.Fatalf("execute review: %v", err)
	}
	if err := e.Store.CancelRun(runID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := e.RejectStep(runID, "review", "too late"); !errors.Is(err, ErrNoAwaitingStep) {
		t.Fatalf("expected ErrNoAwaitingStep after cancel, got %v", err)
	}
}

// TestHeartbeatKeepsLongStepAlive: a step that runs longer than the reaper's
// stale window must NOT be requeued — the in-flight heartbeat keeps the claim.
// Without the fix the reaper requeues it and it re-runs (a duplicate paid LLM
// call for an agent step). Regression test for the live-E2E finding.
func TestHeartbeatKeepsLongStepAlive(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	wf, _ := Parse([]byte("id: slow\nversion: 1.0.0\nsteps:\n  - id: work\n    type: slow\n"))
	e := NewEngine(st, MapLoader{"slow": wf}, t.TempDir())
	e.HeartbeatInterval = 10 * time.Millisecond
	e.Register("slow", slowRunner{d: 300 * time.Millisecond}) // runs >> stale window

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.WorkerLoop(ctx, "w")
	go e.ReaperLoop(ctx, 80, 20*time.Millisecond) // stale after 80ms, checked every 20ms

	runID, err := e.StartRun("slow", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for terminal (or fail on timeout).
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, _ := e.Store.GetRun(runID)
		if store.IsTerminal(run.Status) {
			if run.Status != store.StatusDone {
				t.Fatalf("expected DONE, got %s", run.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not finish in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The step ran exactly once (claimed once, never reaped/re-run).
	tasks, _ := e.Store.TasksForRun(runID)
	if len(tasks) != 1 {
		t.Fatalf("expected exactly 1 task (no requeue), got %d", len(tasks))
	}
	if tasks[0].Attempts != 1 {
		t.Fatalf("expected 1 attempt (heartbeat prevented reaping), got %d", tasks[0].Attempts)
	}
	// No stale-requeue event.
	events, _ := e.Store.EventsAfter(runID, 0)
	for _, ev := range events {
		if containsStr(ev.Data, "stale") {
			t.Fatalf("step was reaped despite heartbeat: %s", ev.Data)
		}
	}
}

// TestEngineWorkdirAbsolute: run workdirs MUST be absolute. A relative workdir
// breaks the docker sandbox (Docker treats a relative -v source as an invalid
// named volume) and is fragile in general. Pins the root-cause fix.
func TestEngineWorkdirAbsolute(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	e := NewEngine(st, nil, ".vibeforge-runs") // deliberately relative
	if !filepath.IsAbs(e.WorkdirRoot) {
		t.Fatalf("WorkdirRoot must be absolute, got %q", e.WorkdirRoot)
	}
	if !filepath.IsAbs(e.Workdir("run_x")) {
		t.Fatalf("per-run workdir must be absolute, got %q", e.Workdir("run_x"))
	}
}

// TestDemoWorkflowFromYAML: the demo manifest (echo -> gate) runs E2E with no
// flow-specific code. This is the core thesis check.
func TestDemoWorkflowFromYAML(t *testing.T) {
	wf, err := ParseFile(filepath.Join("..", "..", "registry", "workflows", "demo.yaml"))
	if err != nil {
		t.Fatalf("parse demo.yaml: %v", err)
	}
	e := newEngine(t, MapLoader{"demo": wf})

	runID, err := e.StartRun("demo", map[string]any{"name": "hello"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusDone {
		t.Fatalf("expected DONE, got %s", status)
	}

	// The marker the gate checked should exist in the run workdir.
	if _, err := os.Stat(filepath.Join(e.Workdir(runID), "marker.txt")); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
}

// TestAddingStepChangesFlowNoCode: define a workflow inline with an extra step.
// The same generic engine runs it — proving a step added in data changes the
// flow without recompiling logic.
func TestAddingStepChangesFlowNoCode(t *testing.T) {
	src := []byte(`
id: three
version: 1.0.0
steps:
  - id: a
    type: echo
    inputs: { write_file: a.txt, write_content: "A" }
  - id: b
    type: echo
    inputs: { write_file: b.txt, write_content: "B" }
  - id: gate
    type: gate
    command: "test -f a.txt && test -f b.txt"
`)
	wf, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e := newEngine(t, MapLoader{"three": wf})
	runID, _ := e.StartRun("three", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil || status != store.StatusDone {
		t.Fatalf("expected DONE, got %s err=%v", status, err)
	}
	// All three steps ran (a, b, gate) — 3 tasks, all DONE.
	tasks, _ := e.Store.TasksForRun(runID)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
}

// TestOnFailLoopRecovers: a gate that fails once then passes. on_fail goto loops
// back to implement; the second gate run passes; run ends DONE.
func TestOnFailLoopRecovers(t *testing.T) {
	// Gate increments a counter file and fails until it reaches 2.
	src := []byte(`
id: recover
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: gate
    type: gate
    command: |
      n=$(cat .n 2>/dev/null || echo 0); n=$((n+1)); echo $n > .n; [ "$n" -ge 2 ]
    on_fail:
      goto: implement
      max: 3
      feedback: $gate.detail
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"recover": wf})
	runID, _ := e.StartRun("recover", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusDone {
		t.Fatalf("expected DONE after recovery, got %s", status)
	}
	// gate should have a failed attempt then a done attempt; implement ran twice.
	tasks, _ := e.Store.TasksForRun(runID)
	var gateFail, gateDone, implementRuns int
	for _, tk := range tasks {
		switch {
		case tk.StepID == "gate" && tk.Status == store.StatusFailed:
			gateFail++
		case tk.StepID == "gate" && tk.Status == store.StatusDone:
			gateDone++
		case tk.StepID == "implement":
			implementRuns++
		}
	}
	if gateFail != 1 || gateDone != 1 {
		t.Fatalf("expected 1 gate fail + 1 gate done, got fail=%d done=%d", gateFail, gateDone)
	}
	if implementRuns != 2 {
		t.Fatalf("expected implement to run twice (loop), got %d", implementRuns)
	}
}

// TestOnFailCapExhausted: a gate that always fails exhausts the cap and the run
// ends FAILED, with exactly max+1 gate attempts.
func TestOnFailCapExhausted(t *testing.T) {
	src := []byte(`
id: cap
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: gate
    type: gate
    command: "false"
    on_fail:
      goto: implement
      max: 2
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"cap": wf})
	runID, _ := e.StartRun("cap", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status != store.StatusFailed {
		t.Fatalf("expected FAILED after cap, got %s", status)
	}
	tasks, _ := e.Store.TasksForRun(runID)
	gateAttempts := 0
	for _, tk := range tasks {
		if tk.StepID == "gate" {
			gateAttempts++
		}
	}
	if gateAttempts != 3 { // max=2 => 3 total attempts
		t.Fatalf("expected 3 gate attempts (max+1), got %d", gateAttempts)
	}
}

// TestRetryGivesFreshOnFailBudget (H3): a run that exhausts on_fail.max ends
// FAILED with max+1 attempts; RetryRun must then give the SAME step a fresh
// budget, not fail immediately because the prior FAILED tasks still exist.
// countFailures is scoped to the retry watermark, so the retry produces another
// full max+1 attempts before failing again.
func TestRetryGivesFreshOnFailBudget(t *testing.T) {
	src := []byte(`
id: retrybudget
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: gate
    type: gate
    command: "false"
    on_fail:
      goto: implement
      max: 2
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"retrybudget": wf})
	// Monotonic clock: every now() call advances 1ms so the retry boundary lands
	// strictly after the first run's FAILED tasks (deterministic watermark).
	var ms int64 = 1_000_000
	e.Store.Now = func() time.Time { ms++; return time.UnixMilli(ms) }

	runID, _ := e.StartRun("retrybudget", nil)
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if status != store.StatusFailed {
		t.Fatalf("expected FAILED after cap, got %s", status)
	}
	countGate := func() int {
		tasks, _ := e.Store.TasksForRun(runID)
		n := 0
		for _, tk := range tasks {
			if tk.StepID == "gate" {
				n++
			}
		}
		return n
	}
	if got := countGate(); got != 3 {
		t.Fatalf("expected 3 gate attempts before retry, got %d", got)
	}

	// Retry: the gate still always fails. A fresh budget => 3 MORE attempts, not
	// an immediate failure with zero retries. Total gate attempts becomes 6.
	if err := e.RetryRun(runID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	status, err = e.RunToCompletion(context.Background(), runID)
	if err != nil {
		t.Fatalf("retried run: %v", err)
	}
	if status != store.StatusFailed {
		t.Fatalf("expected FAILED after retry cap, got %s", status)
	}
	if got := countGate(); got != 6 {
		t.Fatalf("expected 6 gate attempts after retry (3 + a fresh 3), got %d", got)
	}
}

// TestFeedbackInjectedIntoGotoTarget: on_fail feedback reaches the retried step's
// inputs as "feedback".
func TestFeedbackInjectedIntoGotoTarget(t *testing.T) {
	src := []byte(`
id: fb
version: 1.0.0
steps:
  - id: implement
    type: echo
  - id: gate
    type: gate
    command: |
      n=$(cat .n 2>/dev/null || echo 0); n=$((n+1)); echo $n > .n; [ "$n" -ge 2 ]
    on_fail:
      goto: implement
      max: 2
      feedback: $gate.detail
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"fb": wf})
	runID, _ := e.StartRun("fb", nil)
	if _, err := e.RunToCompletion(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	// The second implement task's payload should carry a non-empty feedback.
	tasks, _ := e.Store.TasksForRun(runID)
	var sawFeedback bool
	for _, tk := range tasks {
		if tk.StepID == "implement" && containsStr(tk.Payload, `"feedback"`) {
			sawFeedback = true
		}
	}
	if !sawFeedback {
		t.Fatalf("expected feedback injected into retried implement inputs")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- Bug 3: DirLoader cache invalidation on registry PUT --------------------

// TestDirLoaderInvalidate: saving a changed manifest then calling Invalidate
// makes the next Load return the updated workflow (not the stale cached parse).
func TestDirLoaderInvalidate(t *testing.T) {
	dir := t.TempDir()

	v1 := []byte("id: myflow\nversion: 1.0.0\nsteps:\n  - id: alpha\n    type: echo\n")
	path := filepath.Join(dir, "myflow.yaml")
	if err := os.WriteFile(path, v1, 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDirLoader(dir)

	// First Load: caches v1.
	wf1, err := loader.Load("myflow")
	if err != nil {
		t.Fatalf("load v1: %v", err)
	}
	if len(wf1.Steps) != 1 || wf1.Steps[0].ID != "alpha" {
		t.Fatalf("v1 first step: want alpha, got %+v", wf1.Steps)
	}

	// Save a new version with a different first step.
	v2 := []byte("id: myflow\nversion: 2.0.0\nsteps:\n  - id: beta\n    type: echo\n")
	if err := os.WriteFile(path, v2, 0o644); err != nil {
		t.Fatal(err)
	}

	// Without Invalidate, Load returns the stale cached parse.
	wfStale, _ := loader.Load("myflow")
	if wfStale.Steps[0].ID != "alpha" {
		t.Error("expected stale cache to return alpha (pre-invalidation)")
	}

	// Invalidate and load again: must return the new version.
	loader.Invalidate("myflow")
	wf2, err := loader.Load("myflow")
	if err != nil {
		t.Fatalf("load v2: %v", err)
	}
	if len(wf2.Steps) != 1 || wf2.Steps[0].ID != "beta" {
		t.Fatalf("v2 first step after invalidate: want beta, got %+v", wf2.Steps)
	}
}

// TestEngineInvalidateWorkflow: Engine.InvalidateWorkflow delegates to the
// loader if it supports invalidation, and is a no-op for loaders that don't.
func TestEngineInvalidateWorkflow(t *testing.T) {
	dir := t.TempDir()

	v1 := []byte("id: wf\nversion: 1.0.0\nsteps:\n  - id: step1\n    type: echo\n")
	if err := os.WriteFile(filepath.Join(dir, "wf.yaml"), v1, 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDirLoader(dir)
	eng := newEngine(t, loader)

	// Prime the cache.
	if _, err := loader.Load("wf"); err != nil {
		t.Fatal(err)
	}

	// Overwrite on disk.
	v2 := []byte("id: wf\nversion: 2.0.0\nsteps:\n  - id: step2\n    type: echo\n")
	if err := os.WriteFile(filepath.Join(dir, "wf.yaml"), v2, 0o644); err != nil {
		t.Fatal(err)
	}

	// Engine.InvalidateWorkflow must bust the cache.
	eng.InvalidateWorkflow("wf")

	wf, err := loader.Load("wf")
	if err != nil {
		t.Fatalf("load after engine invalidate: %v", err)
	}
	if len(wf.Steps) == 0 || wf.Steps[0].ID != "step2" {
		t.Fatalf("after engine invalidate: want step2, got %+v", wf.Steps)
	}
}

// driveToAwaiting runs ExecuteOne until a task for the run parks (AWAITING) or no
// more work is claimable. Returns the awaiting step id. Used to drive a human_gate
// (parkRunner) to its parked state in the answer-verb tests.
func driveToAwaiting(t *testing.T, e *Engine, runID string) string {
	t.Helper()
	for i := 0; i < 200; i++ {
		tasks, _ := e.Store.TasksForRun(runID)
		for _, tk := range tasks {
			if tk.Status == store.StatusAwaiting {
				return tk.StepID
			}
		}
		busy, err := e.ExecuteOne(context.Background(), "w")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !busy {
			break
		}
	}
	t.Fatalf("run %s never reached AWAITING", runID)
	return ""
}

const answerFlowSrc = `
id: answerflow
version: 1.0.0
steps:
  - id: prd
    type: echo
  - id: prd_gate
    type: human_gate
    on_fail:
      goto: prd
      max: 2
      feedback: $prd_gate.detail
`

// TestAnswerReRunsPhaseAndReparksGate is the acceptance criterion for the `answer`
// verb: answering a gate's open questions re-runs the phase (goto target) with the
// text injected as `answers` (NOT feedback), and the gate RE-PARKS for approval —
// the run does not auto-advance. It is not a rejection: no FAILED gate task, and the
// on_fail (reject) budget is untouched. Approving afterwards still completes the run.
func TestAnswerReRunsPhaseAndReparksGate(t *testing.T) {
	wf, _ := Parse([]byte(answerFlowSrc))
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("human_gate", parkRunner{})

	runID, _ := e.StartRun("answerflow", nil)
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("expected prd_gate awaiting, got %q", step)
	}

	if err := e.AnswerStep(runID, "prd_gate", "Use Postgres, region us-east"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	// The phase re-runs, then the gate re-parks (reappears — no auto-advance).
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("expected prd_gate awaiting again after answer, got %q", step)
	}

	tasks, _ := e.Store.TasksForRun(runID)
	var prdRuns, gateAwaiting, gateFailed int
	var sawAnswers, sawFeedback bool
	for _, tk := range tasks {
		switch tk.StepID {
		case "prd":
			prdRuns++
			if containsStr(tk.Payload, `"answers"`) {
				sawAnswers = true
			}
			if containsStr(tk.Payload, `"feedback"`) {
				sawFeedback = true
			}
		case "prd_gate":
			switch tk.Status {
			case store.StatusAwaiting:
				gateAwaiting++
			case store.StatusFailed:
				gateFailed++
			}
		}
	}
	if prdRuns != 2 {
		t.Fatalf("expected prd to run twice (initial + after answer), got %d", prdRuns)
	}
	if !sawAnswers {
		t.Fatalf("expected `answers` injected into the re-run prd inputs")
	}
	if sawFeedback {
		t.Fatalf("answer must NOT inject `feedback` (reject semantics leaked into answer)")
	}
	if gateAwaiting != 1 {
		t.Fatalf("expected exactly one prd_gate re-parked (AWAITING), got %d", gateAwaiting)
	}
	if gateFailed != 0 {
		t.Fatalf("answer must not FAIL the gate, got %d FAILED", gateFailed)
	}
	if n, _ := e.countFailures(runID, "prd_gate"); n != 0 {
		t.Fatalf("on_fail (reject) counter must be 0 after an answer, got %d", n)
	}

	// Approve now: the approve path is unchanged and completes the run.
	if err := e.ApproveStep(runID, "prd_gate"); err != nil {
		t.Fatalf("approve after answer: %v", err)
	}
	for i := 0; i < 20; i++ {
		if busy, _ := e.ExecuteOne(context.Background(), "w"); !busy {
			break
		}
	}
	run, _ := e.Store.GetRun(runID)
	if run.Status != store.StatusDone {
		t.Fatalf("expected run DONE after approve, got %s", run.Status)
	}
}

// TestAnswerDoesNotConsumeOnFail: answering many times (well past on_fail.max=2)
// never consumes the reject on_fail budget — answers resolve the gate DONE, so
// countFailures stays 0. Each answer is recorded (CountAnswers) for the cap.
func TestAnswerDoesNotConsumeOnFail(t *testing.T) {
	wf, _ := Parse([]byte(answerFlowSrc))
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("human_gate", parkRunner{})
	runID, _ := e.StartRun("answerflow", nil)

	const rounds = 5 // > on_fail.max (2)
	for i := 0; i < rounds; i++ {
		if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
			t.Fatalf("round %d: expected prd_gate awaiting, got %q", i, step)
		}
		if err := e.AnswerStep(runID, "prd_gate", "answer round"); err != nil {
			t.Fatalf("round %d answer: %v", i, err)
		}
	}
	if n, _ := e.countFailures(runID, "prd_gate"); n != 0 {
		t.Fatalf("on_fail counter must stay 0 after %d answers, got %d", rounds, n)
	}
	if n, _ := e.Store.CountAnswers(runID, "prd_gate"); n != rounds {
		t.Fatalf("expected %d recorded answers, got %d", rounds, n)
	}
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("gate must remain governable after answers, got %q", step)
	}
}

// TestAnswerCapReached: after maxAnswersPerPhase answers, the next answer is
// rejected with ErrAnswerCapReached (the hard anti-loop backstop) and the gate is
// left awaiting (the rejected answer has no side effect).
func TestAnswerCapReached(t *testing.T) {
	wf, _ := Parse([]byte(answerFlowSrc))
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("human_gate", parkRunner{})
	runID, _ := e.StartRun("answerflow", nil)

	for i := 0; i < maxAnswersPerPhase; i++ {
		if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
			t.Fatalf("round %d: expected prd_gate awaiting, got %q", i, step)
		}
		if err := e.AnswerStep(runID, "prd_gate", "answer"); err != nil {
			t.Fatalf("round %d answer: %v", i, err)
		}
	}
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("expected prd_gate awaiting at cap, got %q", step)
	}
	if err := e.AnswerStep(runID, "prd_gate", "one too many"); !errors.Is(err, ErrAnswerCapReached) {
		t.Fatalf("expected ErrAnswerCapReached at cap, got %v", err)
	}
	// The gate is still awaiting — a capped answer must be a no-op.
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("gate must stay awaiting after a capped answer, got %q", step)
	}
}

// TestAnswerNoTarget: a gate with no on_fail.goto has nowhere to route answers →
// ErrNoAnswerTarget, and the gate stays awaiting (never resolved).
func TestAnswerNoTarget(t *testing.T) {
	src := []byte(`
id: answernotarget
version: 1.0.0
steps:
  - id: prd
    type: echo
  - id: prd_gate
    type: human_gate
`)
	wf, _ := Parse(src)
	e := newEngine(t, MapLoader{"answernotarget": wf})
	e.Register("human_gate", parkRunner{})
	runID, _ := e.StartRun("answernotarget", nil)
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("expected prd_gate awaiting, got %q", step)
	}
	if err := e.AnswerStep(runID, "prd_gate", "x"); !errors.Is(err, ErrNoAnswerTarget) {
		t.Fatalf("expected ErrNoAnswerTarget, got %v", err)
	}
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("gate must remain awaiting after a no-target answer, got %q", step)
	}
}

// TestAnswerRacingCancelIsTyped: answering a gate whose run was cancelled returns
// ErrNoAwaitingStep (the same winner-selection typing as approve/reject), not a
// misleading success.
func TestAnswerRacingCancelIsTyped(t *testing.T) {
	wf, _ := Parse([]byte(answerFlowSrc))
	e := newEngine(t, MapLoader{"answerflow": wf})
	e.Register("human_gate", parkRunner{})
	runID, _ := e.StartRun("answerflow", nil)
	if step := driveToAwaiting(t, e, runID); step != "prd_gate" {
		t.Fatalf("expected prd_gate awaiting, got %q", step)
	}
	if err := e.Store.CancelRun(runID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := e.AnswerStep(runID, "prd_gate", "too late"); !errors.Is(err, ErrNoAwaitingStep) {
		t.Fatalf("expected ErrNoAwaitingStep after cancel, got %v", err)
	}
}
