package orchestrator

import (
	"context"
	"testing"
)

// reviewSetup builds project p1 with a DONE SP1 (all stories done → a complete,
// unreviewed increment) and a ready SP2 (all backlog), plus a cp in sprint mode. The
// caller sets p1's review (and optionally planning) mode.
func reviewSetup() (*fakeStoryProvider, *fakeControlPlane) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "done", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x"},
		&fakeStory{id: "B", title: "B", status: "done", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x"},
		&fakeStory{id: "C", title: "C", status: "backlog", sprintID: "SP2", projectID: "p1", repo: "github.com/acme/x"},
		&fakeStory{id: "D", title: "D", status: "backlog", sprintID: "SP2", projectID: "p1", repo: "github.com/acme/x", deps: []string{"C"}},
	)
	cp := &fakeControlPlane{execUnit: "sprint"}
	return provider, cp
}

// TestReviewGatesNextSprintAndIsIdempotent is the acceptance core: in review-ceremony
// mode a DONE sprint (SP1) starts ONE sprint-review run, and the NEXT sprint (SP2) is
// HELD — not fired — until the increment is accepted (reviewed_at stamped). A second
// cycle does not start another review run (idempotent via the persisted review_run_id).
func TestReviewGatesNextSprintAndIsIdempotent(t *testing.T) {
	provider, cp := reviewSetup()
	cp.setReviewMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	// ── Cycle 1: start the review ceremony for SP1, do NOT fire SP2.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 1 {
		t.Fatalf("cycle 1: want 1 sprint-review run, got %d (%v)", got, cp.firedWorkflows())
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 1: SP2 must NOT fire while SP1 awaits review, got %d factory runs", got)
	}
	for _, id := range []string{"C", "D"} {
		if provider.statusOf(id) != "backlog" {
			t.Errorf("cycle 1: %s should still be backlog (SP2 held), got %s", id, provider.statusOf(id))
		}
	}
	if provider.reviewRunCalls != 1 {
		t.Fatalf("cycle 1: review run reference should be persisted once, got %d", provider.reviewRunCalls)
	}

	// The review run's payload carries the reviewed sprint + a story snapshot + the
	// next sprint (where corrections land).
	p := cp.payloadOf(0)
	if p["sprint_id"] != "SP1" {
		t.Errorf("review payload sprint_id = %v, want SP1", p["sprint_id"])
	}
	if snap, _ := p["stories_snapshot"].(string); snap == "" {
		t.Error("review payload should carry a stories_snapshot")
	}
	if p["next_sprint"] != "SP2" {
		t.Errorf("review payload next_sprint = %v, want SP2", p["next_sprint"])
	}

	// ── Cycle 2: the review run is still live (RUNNING) → NO second run, SP2 still held.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 1 {
		t.Fatalf("cycle 2: idempotency broken — want 1 sprint-review run, got %d", got)
	}
	if provider.reviewRunCalls != 1 {
		t.Fatalf("cycle 2: no new review reference should be persisted, got %d", provider.reviewRunCalls)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 2: SP2 still must not fire, got %d factory runs", got)
	}

	// ── Increment accepted: reviewed_at stamped → SP2 unblocks and fires.
	provider.markSprintReviewed("SP1", 123456)

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 1 {
		t.Fatalf("cycle 3: accepted increment should let SP2 fire exactly one factory run, got %d (%v)", got, cp.firedWorkflows())
	}
	for _, id := range []string{"C", "D"} {
		if provider.statusOf(id) != "running" {
			t.Errorf("cycle 3: %s should be claimed running, got %s", id, provider.statusOf(id))
		}
	}
}

// TestReviewLastSprintNoSuccessor covers point 4: the LAST sprint has no ready
// successor to gate it, so the review derivation is a GLOBAL sweep — a DONE unreviewed
// sprint in a ceremony project still generates its review run even with no next sprint.
func TestReviewLastSprintNoSuccessor(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "done", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x"},
		&fakeStory{id: "B", title: "B", status: "done", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x"},
	)
	cp := &fakeControlPlane{execUnit: "sprint"}
	cp.setReviewMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	// No ready sprint/story exists — the project would not enter the fire loop at all,
	// yet the sweep still reviews the last DONE sprint.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 1 {
		t.Fatalf("last-sprint review: want 1 sprint-review run, got %d (%v)", got, cp.firedWorkflows())
	}

	// Idempotent across a second cycle (run still live).
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 1 {
		t.Fatalf("last-sprint review: idempotency broken, got %d", got)
	}
	if provider.reviewRunCalls != 1 {
		t.Fatalf("last-sprint review: want 1 persisted review reference, got %d", provider.reviewRunCalls)
	}
}

// TestAutoReviewModeNoCeremony: with review_mode auto (the default), a DONE sprint is
// accepted implicitly — no review run — and the next sprint fires normally.
func TestAutoReviewModeNoCeremony(t *testing.T) {
	provider, cp := reviewSetup() // review_mode defaults to auto
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 0 {
		t.Fatalf("auto mode must not run the review ceremony, got %d sprint-review runs", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 1 {
		t.Fatalf("auto mode should fire SP2 immediately, got %d factory runs", got)
	}
	if provider.reviewRunCalls != 0 {
		t.Fatalf("auto mode should persist no review reference, got %d", provider.reviewRunCalls)
	}
}

// TestReviewRereviewsAfterTerminalFailure: if the recorded review run ended terminally
// without acceptance (reviewed_at still 0), the scheduler starts a fresh review run
// rather than wedging the project behind an unaccepted increment.
func TestReviewRereviewsAfterTerminalFailure(t *testing.T) {
	provider, cp := reviewSetup()
	cp.setReviewMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// The first review run is run-1; mark it FAILED (a crashed reporter, say).
	cp.setStatus(fakeRunID(1), "FAILED")

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 2 {
		t.Fatalf("a terminally-failed review run should be re-reviewed, got %d sprint-review runs", got)
	}
}

// TestReviewHoldsPlanningOfNextSprint: an unaccepted increment blocks BOTH the planning
// ceremony and the fire of the next sprint (order: review(N-1) → planning(N) → fire(N)).
func TestReviewHoldsPlanningOfNextSprint(t *testing.T) {
	provider, cp := reviewSetup()
	// p1 runs BOTH ceremonies. While SP1 is unreviewed, SP2 must not even start planning.
	cp.setReviewMode("p1", "sprint", "ceremony")
	cp.setPlanningMode("p1", "sprint", "ceremony")
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-review"); got != 1 {
		t.Fatalf("cycle 1: want 1 sprint-review run, got %d", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-planning"); got != 0 {
		t.Fatalf("cycle 1: SP2 planning must be held while SP1 awaits review, got %d planning runs", got)
	}

	// Accept SP1 → SP2 planning now proceeds.
	provider.markSprintReviewed("SP1", 123456)
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-planning"); got != 1 {
		t.Fatalf("cycle 2: accepted increment should let SP2 planning start, got %d planning runs", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 2: SP2 must plan before it fires, got %d factory runs", got)
	}
}
