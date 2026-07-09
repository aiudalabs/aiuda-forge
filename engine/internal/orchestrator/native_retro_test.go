package orchestrator

import (
	"context"
	"testing"
)

// TestRetroGatesNextSprintAndIsIdempotent: in retro-ceremony mode a REVIEWED sprint
// (SP1) starts ONE retro run, and the next sprint (SP2) is HELD until the method
// improvement is applied (retro_at stamped). A second cycle starts no second run.
func TestRetroGatesNextSprintAndIsIdempotent(t *testing.T) {
	provider, cp := reviewSetup() // SP1 done, SP2 ready, p1 sprint mode
	provider.markSprintReviewed("SP1", 111)
	cp.setRetroMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	// ── Cycle 1: start the retro for SP1, do NOT fire SP2.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 1 {
		t.Fatalf("cycle 1: want 1 retro run, got %d (%v)", got, cp.firedWorkflows())
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 1: SP2 must NOT fire while SP1 awaits retro, got %d factory runs", got)
	}
	if provider.retroRunCalls != 1 {
		t.Fatalf("cycle 1: retro run reference should be persisted once, got %d", provider.retroRunCalls)
	}
	p := cp.payloadOf(0)
	if p["sprint_id"] != "SP1" {
		t.Errorf("retro payload sprint_id = %v, want SP1", p["sprint_id"])
	}
	if tel, _ := p["telemetry"].(string); tel == "" {
		t.Error("retro payload should carry telemetry")
	}

	// ── Cycle 2: the retro run is still live → NO second run, SP2 still held.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 1 {
		t.Fatalf("cycle 2: idempotency broken — want 1 retro run, got %d", got)
	}
	if provider.retroRunCalls != 1 {
		t.Fatalf("cycle 2: no new retro reference should be persisted, got %d", provider.retroRunCalls)
	}

	// ── Method applied: retro_at stamped → SP2 unblocks and fires.
	provider.markSprintRetroed("SP1", 222)
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 1 {
		t.Fatalf("cycle 3: applied retro should let SP2 fire, got %d factory runs (%v)", got, cp.firedWorkflows())
	}
}

// TestRetroLastSprintNoSuccessor: the LAST reviewed sprint (no ready successor) is still
// retro'd — the derivation is a global sweep, not gated on ready work.
func TestRetroLastSprintNoSuccessor(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "done", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x"},
	)
	provider.markSprintReviewed("SP1", 111)
	cp := &fakeControlPlane{execUnit: "sprint"}
	cp.setRetroMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 1 {
		t.Fatalf("last-sprint retro: want 1 retro run, got %d", got)
	}
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 1 {
		t.Fatalf("last-sprint retro: idempotency broken, got %d", got)
	}
}

// TestAutoRetroModeNoCeremony: with retro_mode auto (default), a reviewed sprint runs no
// retro and the next sprint fires normally.
func TestAutoRetroModeNoCeremony(t *testing.T) {
	provider, cp := reviewSetup()
	provider.markSprintReviewed("SP1", 111) // reviewed, but retro auto
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 0 {
		t.Fatalf("auto mode must not run the retro, got %d retro runs", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 1 {
		t.Fatalf("auto mode should fire SP2, got %d factory runs", got)
	}
	if provider.retroRunCalls != 0 {
		t.Fatalf("auto mode should persist no retro reference, got %d", provider.retroRunCalls)
	}
}

// TestRetroRereviewsAfterTerminalFailure: a terminally-failed retro run (retro_at still 0)
// is re-run rather than wedging the project.
func TestRetroRereviewsAfterTerminalFailure(t *testing.T) {
	provider, cp := reviewSetup()
	provider.markSprintReviewed("SP1", 111)
	cp.setRetroMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	cp.setStatus(fakeRunID(1), "FAILED")
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 2 {
		t.Fatalf("a terminally-failed retro run should be re-run, got %d retro runs", got)
	}
}

// TestReviewThenRetroThenFireOrder verifies the full ceremony order for a project in
// BOTH review and retro ceremony: review(N-1) → retro(N-1) → fire(N). Review must land
// (reviewed_at) before retro even starts (retro gates on reviewed_at), and BOTH must
// complete before the next sprint fires.
func TestReviewThenRetroThenFireOrder(t *testing.T) {
	provider, cp := reviewSetup() // SP1 DONE (unreviewed), SP2 ready
	cp.setReviewMode("p1", "sprint", "ceremony")
	cp.setRetroMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	// Cycle 1: SP1 is done but unreviewed → review fires; retro does NOT (gated on
	// reviewed_at); SP2 held.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 1 {
		t.Fatalf("cycle 1: want 1 review run, got %d", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 0 {
		t.Fatalf("cycle 1: retro must wait for review, got %d retro runs", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 1: SP2 must be held, got %d factory runs", got)
	}

	// Review accepted → cycle 2: retro fires; SP2 still held; no second review.
	provider.markSprintReviewed("SP1", 111)
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "retro"); got != 1 {
		t.Fatalf("cycle 2: want 1 retro run after review, got %d", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 2: SP2 must still be held for retro, got %d factory runs", got)
	}

	// Retro applied → cycle 3: SP2 fires.
	provider.markSprintRetroed("SP1", 222)
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 1 {
		t.Fatalf("cycle 3: SP2 should fire once both review and retro are done, got %d", got)
	}
}
