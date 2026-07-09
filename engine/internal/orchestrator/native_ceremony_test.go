package orchestrator

import (
	"context"
	"testing"
)

func countWorkflow(wfs []string, name string) int {
	n := 0
	for _, w := range wfs {
		if w == name {
			n++
		}
	}
	return n
}

// ceremonyProvider builds a two-story SP1 sprint in project p1 (all backlog, so the
// sprint is ready), plus a cp in sprint+ceremony mode.
func ceremonySetup() (*fakeStoryProvider, *fakeControlPlane) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "backlog", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x"},
		&fakeStory{id: "B", title: "B", status: "backlog", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x", deps: []string{"A"}},
	)
	cp := &fakeControlPlane{execUnit: "sprint"}
	cp.setPlanningMode("p1", "sprint", "ceremony")
	return provider, cp
}

// TestCeremonyGatesSprintAndIsIdempotent is the acceptance core: in ceremony mode a
// ready sprint is NOT fired; instead ONE planning run is started, and a second
// scheduler cycle does not start another (idempotent via the persisted
// planning_run_id). Once planned_at is stamped, the sprint fires normally.
func TestCeremonyGatesSprintAndIsIdempotent(t *testing.T) {
	provider, cp := ceremonySetup()
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	// ── Cycle 1: start the planning ceremony, do NOT fire the sprint.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-planning"); got != 1 {
		t.Fatalf("cycle 1: want 1 sprint-planning run, got %d (%v)", got, cp.firedWorkflows())
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 1: the sprint must NOT be fired, got %d factory runs", got)
	}
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) != "backlog" {
			t.Errorf("cycle 1: %s should still be backlog (not claimed), got %s", id, provider.statusOf(id))
		}
	}
	if provider.planningRunCalls != 1 {
		t.Fatalf("cycle 1: planning run reference should be persisted once, got %d", provider.planningRunCalls)
	}

	// The planning run's payload carries the sprint id + a live backlog snapshot.
	p := cp.payloadOf(0)
	if p["sprint_id"] != "SP1" {
		t.Errorf("planning payload sprint_id = %v, want SP1", p["sprint_id"])
	}
	if snap, _ := p["backlog_snapshot"].(string); snap == "" {
		t.Error("planning payload should carry a backlog_snapshot")
	}

	// ── Cycle 2: the planning run is still live (RUNNING) → NO second run, NO claim.
	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-planning"); got != 1 {
		t.Fatalf("cycle 2: idempotency broken — want 1 sprint-planning run, got %d", got)
	}
	if provider.planningRunCalls != 1 {
		t.Fatalf("cycle 2: no new planning reference should be persisted, got %d", provider.planningRunCalls)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 0 {
		t.Fatalf("cycle 2: still must not fire the sprint, got %d factory runs", got)
	}

	// ── Plan approved + applied: planned_at is stamped → the sprint unblocks.
	provider.markSprintPlanned("SP1", 123456)

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 1 {
		t.Fatalf("cycle 3: planned sprint should fire exactly one factory run, got %d (%v)", got, cp.firedWorkflows())
	}
	for _, id := range []string{"A", "B"} {
		if provider.statusOf(id) != "running" {
			t.Errorf("cycle 3: %s should be claimed running, got %s", id, provider.statusOf(id))
		}
	}
}

// TestCeremonyReplansAfterTerminalFailure: if the recorded planning run ended
// terminally without producing a plan (planned_at still 0), the scheduler starts a
// fresh planning run rather than wedging the sprint.
func TestCeremonyReplansAfterTerminalFailure(t *testing.T) {
	provider, cp := ceremonySetup()
	sched := NewNativeScheduler(provider, cp, "factory", nil)
	ctx := context.Background()

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// The first planning run is run-1; mark it FAILED (a crashed planner, say).
	cp.setStatus(fakeRunID(1), "FAILED")

	if _, err := sched.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-planning"); got != 2 {
		t.Fatalf("a terminally-failed planning run should be re-planned, got %d sprint-planning runs", got)
	}
}

// TestAutoModeFiresSprintNoCeremony: with planning_mode auto (the default), a ready
// sprint fires immediately and NO planning run is started — the ceremony is inert.
func TestAutoModeFiresSprintNoCeremony(t *testing.T) {
	provider := newFakeProvider(
		&fakeStory{id: "A", title: "A", status: "backlog", sprintID: "SP1", projectID: "p1", repo: "github.com/acme/x"},
	)
	cp := &fakeControlPlane{execUnit: "sprint"} // planning_mode defaults to auto
	sched := NewNativeScheduler(provider, cp, "factory", nil)

	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := countWorkflow(cp.firedWorkflows(), "sprint-planning"); got != 0 {
		t.Fatalf("auto mode must not run the ceremony, got %d sprint-planning runs", got)
	}
	if got := countWorkflow(cp.firedWorkflows(), "factory"); got != 1 {
		t.Fatalf("auto mode should fire the sprint immediately, got %d factory runs", got)
	}
	if provider.statusOf("A") != "running" {
		t.Errorf("auto mode: A should be claimed running, got %s", provider.statusOf("A"))
	}
}
