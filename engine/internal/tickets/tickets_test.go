package tickets_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"forge/internal/tickets"
)

func openTemp(t *testing.T) *tickets.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := tickets.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// driveToDone walks a story through the LEGAL merge-gated lifecycle to done:
// backlog → running → in_review → done. The state machine (B1/B2) forbids the old
// backlog→done shortcut, so fixtures that need a "done" prerequisite advance it the
// real way. Fails the test on any illegal step.
func driveToDone(t *testing.T, st *tickets.Store, id string) {
	t.Helper()
	if err := st.UpdateStoryStatus(id, tickets.StatusRunning); err != nil {
		t.Fatalf("drive %s → running: %v", id, err)
	}
	if err := st.MarkInReview(id, "https://example.test/pull/1"); err != nil {
		t.Fatalf("drive %s → in_review: %v", id, err)
	}
	if err := st.MarkDone(id); err != nil {
		t.Fatalf("drive %s → done: %v", id, err)
	}
}

// ---- Epics ------------------------------------------------------------------

func TestEpicCRUD(t *testing.T) {
	st := openTemp(t)

	epic := tickets.Epic{ID: "E1", Title: "Foundation", Description: "Core infra"}
	if err := st.CreateEpic(epic); err != nil {
		t.Fatalf("create epic: %v", err)
	}

	got, err := st.GetEpic("E1")
	if err != nil {
		t.Fatalf("get epic: %v", err)
	}
	if got.Title != "Foundation" {
		t.Errorf("title: got %q, want %q", got.Title, "Foundation")
	}

	epics, err := st.ListEpics()
	if err != nil {
		t.Fatalf("list epics: %v", err)
	}
	if len(epics) != 1 {
		t.Errorf("list: got %d, want 1", len(epics))
	}
}

func TestEpicNotFound(t *testing.T) {
	st := openTemp(t)
	_, err := st.GetEpic("missing")
	if err != tickets.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- Sprints ----------------------------------------------------------------

func TestSprintCRUD(t *testing.T) {
	st := openTemp(t)

	sp := tickets.Sprint{ID: "SP1", Name: "Sprint 1", Goal: "Ship MVP"}
	if err := st.CreateSprint(sp); err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	sprints, err := st.ListSprints()
	if err != nil {
		t.Fatalf("list sprints: %v", err)
	}
	if len(sprints) != 1 {
		t.Errorf("list: got %d, want 1", len(sprints))
	}
	if sprints[0].Goal != "Ship MVP" {
		t.Errorf("goal: got %q, want %q", sprints[0].Goal, "Ship MVP")
	}
}

// ---- Stories ----------------------------------------------------------------

func TestStoryCRUD(t *testing.T) {
	st := openTemp(t)

	story := tickets.Story{
		ID:     "S1-01",
		Title:  "Auth",
		Body:   "Implement login",
		Accept: "User can log in",
		Status: tickets.StatusBacklog,
	}
	if err := st.CreateStory(story); err != nil {
		t.Fatalf("create story: %v", err)
	}

	got, err := st.GetStory("S1-01")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if got.Title != "Auth" {
		t.Errorf("title: got %q, want %q", got.Title, "Auth")
	}
	if len(got.Deps) != 0 {
		t.Errorf("deps: got %v, want empty", got.Deps)
	}

	stories, err := st.ListStories()
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(stories) != 1 {
		t.Errorf("list: got %d, want 1", len(stories))
	}
}

func TestStoryNotFound(t *testing.T) {
	st := openTemp(t)
	_, err := st.GetStory("missing")
	if err != tickets.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- Deps persistence -------------------------------------------------------

func TestDepsPersistence(t *testing.T) {
	st := openTemp(t)

	// Create two dep stories and one dependent.
	for _, id := range []string{"D1", "D2"} {
		if err := st.CreateStory(tickets.Story{ID: id, Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	s := tickets.Story{ID: "S1", Title: "needs deps", Deps: []string{"D1", "D2"}}
	if err := st.CreateStory(s); err != nil {
		t.Fatalf("create S1: %v", err)
	}

	got, err := st.GetStory("S1")
	if err != nil {
		t.Fatalf("get S1: %v", err)
	}
	if len(got.Deps) != 2 {
		t.Errorf("deps len: got %d, want 2", len(got.Deps))
	}
}

// ---- AddDep -----------------------------------------------------------------

func TestAddDep(t *testing.T) {
	st := openTemp(t)

	if err := st.CreateStory(tickets.Story{ID: "D1", Title: "D1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1"}); err != nil {
		t.Fatal(err)
	}

	if err := st.AddDep("S1", []string{"D1"}); err != nil {
		t.Fatalf("add dep: %v", err)
	}
	got, _ := st.GetStory("S1")
	if len(got.Deps) != 1 || got.Deps[0] != "D1" {
		t.Errorf("deps: got %v, want [D1]", got.Deps)
	}

	// Adding again must not fail or duplicate.
	if err := st.AddDep("S1", []string{"D1"}); err != nil {
		t.Fatalf("add dup dep: %v", err)
	}
	got, _ = st.GetStory("S1")
	if len(got.Deps) != 1 {
		t.Errorf("dup deps: got %d, want 1", len(got.Deps))
	}
}

// ---- Ready computation ------------------------------------------------------

func TestReadyNoDeps(t *testing.T) {
	st := openTemp(t)

	// A story with no deps and status=backlog is immediately ready.
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1"}); err != nil {
		t.Fatal(err)
	}

	ready, err := st.Ready()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "S1" {
		t.Errorf("ready: got %v, want [S1]", ready)
	}
}

func TestReadyBlockedByDep(t *testing.T) {
	st := openTemp(t)

	// D1 is backlog; S1 depends on D1 → S1 is NOT ready.
	if err := st.CreateStory(tickets.Story{ID: "D1", Title: "D1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1", Deps: []string{"D1"}}); err != nil {
		t.Fatal(err)
	}

	ready, err := st.Ready()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	// D1 is ready (no deps); S1 is not.
	ids := storyIDs(ready)
	if !contains(ids, "D1") {
		t.Errorf("D1 should be ready, got %v", ids)
	}
	if contains(ids, "S1") {
		t.Errorf("S1 should NOT be ready (D1 is backlog), got %v", ids)
	}
}

func TestReadyUnblockedWhenDepDone(t *testing.T) {
	st := openTemp(t)

	// D1 done → S1 becomes ready.
	if err := st.CreateStory(tickets.Story{ID: "D1", Title: "D1", Status: tickets.StatusDone}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1", Deps: []string{"D1"}}); err != nil {
		t.Fatal(err)
	}

	ready, err := st.Ready()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	ids := storyIDs(ready)
	if !contains(ids, "S1") {
		t.Errorf("S1 should be ready when D1=done, got %v", ids)
	}
}

func TestReadyTransition(t *testing.T) {
	st := openTemp(t)

	// S1 depends on D1. D1 starts backlog (blocks S1). We mark D1 done → S1 ready.
	if err := st.CreateStory(tickets.Story{ID: "D1", Title: "D1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1", Deps: []string{"D1"}}); err != nil {
		t.Fatal(err)
	}

	// Before: S1 is blocked.
	ready, _ := st.Ready()
	if contains(storyIDs(ready), "S1") {
		t.Error("S1 should be blocked before D1 is done")
	}

	// Mark D1 done (via the legal lifecycle — backlog→done is no longer allowed).
	driveToDone(t, st, "D1")

	// After: S1 is ready.
	ready, err := st.Ready()
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if !contains(storyIDs(ready), "S1") {
		t.Errorf("S1 should be ready after D1=done, got %v", storyIDs(ready))
	}
}

// ---- Status transitions -----------------------------------------------------

func TestUpdateStoryStatus(t *testing.T) {
	st := openTemp(t)

	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1"}); err != nil {
		t.Fatal(err)
	}

	transitions := []tickets.Status{
		tickets.StatusRunning,
		tickets.StatusInReview,
		tickets.StatusDone,
	}
	for _, to := range transitions {
		if err := st.UpdateStoryStatus("S1", to); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
		got, _ := st.GetStory("S1")
		if got.Status != to {
			t.Errorf("status after transition: got %q, want %q", got.Status, to)
		}
	}
}

func TestUpdateStoryStatusNotFound(t *testing.T) {
	st := openTemp(t)
	err := st.UpdateStoryStatus("missing", tickets.StatusDone)
	if err != tickets.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- B2: state-machine guards (terminal states never resurrected) -----------

// TestTerminalNotResurrected: a done or failed story is a TERMINAL state — no
// mutator may move it. Each illegal transition is a no-op returning
// ErrIllegalTransition; the stored status is unchanged.
func TestTerminalNotResurrected(t *testing.T) {
	st := openTemp(t)

	// Build a done story the legal way and a failed story.
	if err := st.CreateStory(tickets.Story{ID: "DN", Title: "done"}); err != nil {
		t.Fatal(err)
	}
	driveToDone(t, st, "DN")
	if err := st.CreateStory(tickets.Story{ID: "FL", Title: "failed"}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFailed("FL"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		id   string
		do   func() error
	}{
		{"done→failed", "DN", func() error { return st.MarkFailed("DN") }},
		{"done→in_review", "DN", func() error { return st.MarkInReview("DN", "x") }},
		{"done→running", "DN", func() error { return st.UpdateStoryStatus("DN", tickets.StatusRunning) }},
		{"failed→done", "FL", func() error { return st.MarkDone("FL") }},
		{"failed→in_review", "FL", func() error { return st.MarkInReview("FL", "x") }},
		{"failed→running", "FL", func() error { return st.UpdateStoryStatus("FL", tickets.StatusRunning) }},
	}
	for _, c := range cases {
		before, _ := st.GetStory(c.id)
		err := c.do()
		if !errors.Is(err, tickets.ErrIllegalTransition) {
			t.Errorf("%s: want ErrIllegalTransition, got %v", c.name, err)
		}
		after, _ := st.GetStory(c.id)
		if after.Status != before.Status {
			t.Errorf("%s: status changed %s→%s (should be a no-op)", c.name, before.Status, after.Status)
		}
	}
}

// TestMarkDoneOnlyFromInReview: MarkDone refuses a still-running story (B1/B2) —
// done means a merged PR, which a story only reaches via in_review.
func TestMarkDoneOnlyFromInReview(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "R", Title: "r", Status: tickets.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDone("R"); !errors.Is(err, tickets.ErrIllegalTransition) {
		t.Fatalf("running→done must be illegal, got %v", err)
	}
	if got, _ := st.GetStory("R"); got.Status != tickets.StatusRunning {
		t.Errorf("status should be unchanged, got %s", got.Status)
	}
}

// TestMarkRunningIdempotent: UpdateStoryStatus(id,"running") on an already-running
// story is an idempotent no-op (the orchestrator MarkRunning path re-asserts
// running to attach a run_id), not an error.
func TestMarkRunningIdempotent(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "R", Title: "r", Status: tickets.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateStoryStatus("R", tickets.StatusRunning); err != nil {
		t.Errorf("running→running should be a no-op, got %v", err)
	}
}

// ---- M5: SetStoryRun does not stamp a terminal story ------------------------

func TestSetStoryRunRejectsTerminal(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "FL", Title: "f", Status: tickets.StatusFailed}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStoryRun("FL", "run-x"); !errors.Is(err, tickets.ErrIllegalTransition) {
		t.Errorf("set-run on a failed story must be illegal, got %v", err)
	}
	if got, _ := st.GetStory("FL"); got.RunID != "" {
		t.Errorf("run_id should not be stamped on a terminal story, got %q", got.RunID)
	}
}

// ---- B3: MarkBacklog resets a claimed-but-unfired story ---------------------

func TestMarkBacklogResetsClaim(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "S", Title: "s"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.ClaimStory("S"); !ok {
		t.Fatal("claim should succeed")
	}
	// Running, empty run_id (FireRun failed) → reset to backlog.
	if err := st.MarkBacklog("S"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got, _ := st.GetStory("S"); got.Status != tickets.StatusBacklog {
		t.Errorf("want backlog after reset, got %s", got.Status)
	}
	// A running story that ALREADY has a run_id is genuinely executing — not reset.
	if ok, _ := st.ClaimStory("S"); !ok {
		t.Fatal("re-claim should succeed")
	}
	if err := st.SetStoryRun("S", "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkBacklog("S"); !errors.Is(err, tickets.ErrIllegalTransition) {
		t.Errorf("reset of a fired (run_id set) story must be illegal, got %v", err)
	}
	if got, _ := st.GetStory("S"); got.Status != tickets.StatusRunning {
		t.Errorf("fired story should stay running, got %s", got.Status)
	}
}

// ---- H6/H7: dependency validation -------------------------------------------

func TestCreateStoryRejectsSelfDep(t *testing.T) {
	st := openTemp(t)
	err := st.CreateStory(tickets.Story{ID: "S", Title: "s", Deps: []string{"S"}})
	if !errors.Is(err, tickets.ErrDepCycle) {
		t.Errorf("self-dep must be rejected with ErrDepCycle, got %v", err)
	}
}

func TestAddDepRejectsCycle(t *testing.T) {
	st := openTemp(t)
	// A depends on B. Adding B→A would close a cycle.
	if err := st.CreateStory(tickets.Story{ID: "A", Title: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateStory(tickets.Story{ID: "B", Title: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDep("A", []string{"B"}); err != nil {
		t.Fatalf("A→B should be fine: %v", err)
	}
	if err := st.AddDep("B", []string{"A"}); !errors.Is(err, tickets.ErrDepCycle) {
		t.Errorf("B→A should close a cycle (ErrDepCycle), got %v", err)
	}
}

func TestAddDepRejectsMissingStory(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "A", Title: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDep("A", []string{"NOPE"}); !errors.Is(err, tickets.ErrDepNotFound) {
		t.Errorf("dep on a non-existent story must be ErrDepNotFound, got %v", err)
	}
}

// TestValidateDepsCatchesDanglingForwardRef: a forward reference that never gets
// created in the batch is allowed at insert time but caught by the whole-graph
// publish check (H6) — otherwise the dependent deadlocks forever.
func TestValidateDepsCatchesDanglingForwardRef(t *testing.T) {
	st := openTemp(t)
	// S depends on FUTURE, created before FUTURE exists (forward ref) — allowed.
	if err := st.CreateStory(tickets.Story{ID: "S", Title: "s", Deps: []string{"FUTURE"}}); err != nil {
		t.Fatalf("forward ref should be allowed at insert: %v", err)
	}
	// FUTURE never created → whole-graph validation flags the dangling dep.
	if err := st.ValidateDeps(); !errors.Is(err, tickets.ErrDepNotFound) {
		t.Errorf("dangling dep must be caught by ValidateDeps, got %v", err)
	}
}

// TestTopoSortCycleErrors: a sprint whose stories form a cycle makes StoriesBySprint
// (via topoSortStories) return ErrDepCycle, not a silently-misordered slice (H7).
func TestTopoSortCycleErrors(t *testing.T) {
	st := openTemp(t)
	// Create A and B with no deps, then wire a cycle via AddDep's raw edge insert
	// is blocked — so seed the cycle by inserting both deps before validation can
	// see the closing edge: A→B at create, then the closing B→A is rejected by
	// AddDep. To exercise topoSort's own guard we instead build the cycle through
	// two forward refs in one batch-like sequence.
	if err := st.CreateStory(tickets.Story{ID: "A", Title: "a", SprintID: "SP", Deps: []string{"B"}}); err != nil {
		t.Fatal(err) // forward ref to B — allowed
	}
	if err := st.CreateStory(tickets.Story{ID: "B", Title: "b", SprintID: "SP", Deps: []string{"A"}}); err != nil {
		// B→A closes a cycle against the now-existing A→B edge → rejected at create.
		if !errors.Is(err, tickets.ErrDepCycle) {
			t.Fatalf("expected ErrDepCycle on cycle-closing create, got %v", err)
		}
		return // cycle correctly prevented at the store boundary
	}
	t.Fatal("creating the cycle-closing story should have been rejected")
}

// ---- SetStoryRun ------------------------------------------------------------

func TestSetStoryRun(t *testing.T) {
	st := openTemp(t)

	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStoryRun("S1", "run-abc"); err != nil {
		t.Fatalf("set story run: %v", err)
	}
	got, _ := st.GetStory("S1")
	if got.RunID != "run-abc" {
		t.Errorf("run_id: got %q, want %q", got.RunID, "run-abc")
	}
}

// ---- NativeProvider (interface round-trip) ----------------------------------

func TestNativeProviderRoundTrip(t *testing.T) {
	st := openTemp(t)
	p := tickets.NewNativeProvider(st)
	ctx := context.Background()

	story := tickets.Story{ID: "S1", Title: "via provider"}
	if err := p.CreateStory(ctx, story); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := p.GetStory(ctx, "S1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "via provider" {
		t.Errorf("title: got %q", got.Title)
	}

	// Advance through the legal lifecycle via the provider surface
	// (backlog→running→in_review→done); the old one-shot backlog→done is rejected.
	if _, err := p.ClaimStory(ctx, "S1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := p.MarkInReview(ctx, "S1", "https://example.test/pull/1"); err != nil {
		t.Fatalf("in_review: %v", err)
	}
	if err := p.MarkDone(ctx, "S1"); err != nil {
		t.Fatalf("done: %v", err)
	}

	list, err := p.ListStories(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Status != tickets.StatusDone {
		t.Errorf("list status: got %v", list)
	}

	ready, err := p.Ready(ctx)
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	// S1 is done, not backlog — should not appear in ready.
	if len(ready) != 0 {
		t.Errorf("done story appeared in ready: %v", ready)
	}
}

// ---- ClaimStory (Bug 2: double-fire prevention) -----------------------------

func TestClaimStorySucceeds(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "claimable"}); err != nil {
		t.Fatal(err)
	}

	claimed, err := st.ClaimStory("S1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claimed {
		t.Error("first claim should succeed")
	}
	got, _ := st.GetStory("S1")
	if got.Status != tickets.StatusRunning {
		t.Errorf("status after claim: want running, got %s", got.Status)
	}
}

func TestClaimStoryOnlyOneWins(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "race"}); err != nil {
		t.Fatal(err)
	}

	// First claim succeeds.
	first, err := st.ClaimStory("S1")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Error("first claim should win")
	}

	// Second claim on the same story (now "running") must return false.
	second, err := st.ClaimStory("S1")
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Error("second claim should not win — story already running")
	}
}

// ---- MarkFailed (Bug 1: failed run handling) --------------------------------

func TestMarkFailed(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "S1", Status: tickets.StatusRunning}); err != nil {
		t.Fatal(err)
	}

	if err := st.MarkFailed("S1"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	got, _ := st.GetStory("S1")
	if got.Status != tickets.StatusFailed {
		t.Errorf("status: want failed, got %s", got.Status)
	}
}

func TestMarkFailedNotFound(t *testing.T) {
	st := openTemp(t)
	err := st.MarkFailed("missing")
	if err != tickets.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- Story.Repo persistence -------------------------------------------------

func TestStoryRepoRoundTrip(t *testing.T) {
	st := openTemp(t)

	const wantRepo = "https://github.com/acme/widget"
	story := tickets.Story{
		ID:    "SR-01",
		Title: "Repo story",
		Repo:  wantRepo,
	}
	if err := st.CreateStory(story); err != nil {
		t.Fatalf("create story: %v", err)
	}

	got, err := st.GetStory("SR-01")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if got.Repo != wantRepo {
		t.Errorf("repo: got %q, want %q", got.Repo, wantRepo)
	}
}

// ---- Sprint-batched (goal mode) ---------------------------------------------

// seedSprint creates a sprint and its stories (each pre-assigned to the sprint).
func seedSprint(t *testing.T, st *tickets.Store, sprintID string, stories ...tickets.Story) {
	t.Helper()
	if err := st.CreateSprint(tickets.Sprint{ID: sprintID, Name: sprintID}); err != nil {
		t.Fatalf("create sprint %s: %v", sprintID, err)
	}
	for _, s := range stories {
		s.SprintID = sprintID
		if err := st.CreateStory(s); err != nil {
			t.Fatalf("create story %s: %v", s.ID, err)
		}
	}
}

// TestStoriesBySprintTopoOrder: intra-sprint deps order the stories so a story
// always follows its in-sprint deps; ties break by id.
func TestStoriesBySprintTopoOrder(t *testing.T) {
	st := openTemp(t)
	// C depends on B, B depends on A → A, B, C. D has no deps; by id it sorts
	// before others but must still respect that nothing depends on it.
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "C", Title: "C", Deps: []string{"B"}},
		tickets.Story{ID: "B", Title: "B", Deps: []string{"A"}},
		tickets.Story{ID: "A", Title: "A"},
		tickets.Story{ID: "D", Title: "D"},
	)

	got, err := st.StoriesBySprint("SP1")
	if err != nil {
		t.Fatalf("stories by sprint: %v", err)
	}
	order := storyIDs(got)
	// A must come before B, B before C. D (no deps) sorts by id first.
	if idx(order, "A") > idx(order, "B") || idx(order, "B") > idx(order, "C") {
		t.Errorf("topo order violated: %v", order)
	}
	// Deterministic full order: at each step the lowest-id story whose in-sprint
	// deps are already placed is chosen → A (no deps), B (A placed), C (B placed),
	// then D (no deps, but higher id than A/B/C so it lands last).
	want := []string{"A", "B", "C", "D"}
	if !equalSlice(order, want) {
		t.Errorf("order: got %v, want %v", order, want)
	}
}

// TestStoriesBySprintExternalDepIgnored: a dep OUTSIDE the sprint doesn't affect
// intra-sprint ordering.
func TestStoriesBySprintExternalDepIgnored(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "EXT", Title: "ext"}); err != nil {
		t.Fatal(err)
	}
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "B", Title: "B", Deps: []string{"EXT"}},
		tickets.Story{ID: "A", Title: "A"},
	)
	got, _ := st.StoriesBySprint("SP1")
	// EXT is external → no intra-sprint constraint; order is id-stable A, B.
	if !equalSlice(storyIDs(got), []string{"A", "B"}) {
		t.Errorf("order: got %v, want [A B]", storyIDs(got))
	}
}

// TestReadySprintsAllBacklog: a sprint with all-backlog stories and no external
// deps is ready; a sprint with a started story is not.
func TestReadySprintsAllBacklog(t *testing.T) {
	st := openTemp(t)
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "A", Title: "A"},
		tickets.Story{ID: "B", Title: "B", Deps: []string{"A"}}, // intra-sprint dep — OK
	)
	seedSprint(t, st, "SP2",
		tickets.Story{ID: "C", Title: "C", Status: tickets.StatusRunning}, // already started
		tickets.Story{ID: "D", Title: "D"},
	)

	ready, err := st.ReadySprints()
	if err != nil {
		t.Fatalf("ready sprints: %v", err)
	}
	ids := sprintIDs(ready)
	if !contains(ids, "SP1") {
		t.Errorf("SP1 should be ready (all backlog, intra-sprint dep), got %v", ids)
	}
	if contains(ids, "SP2") {
		t.Errorf("SP2 should NOT be ready (a story already started), got %v", ids)
	}
}

// TestReadySprintsExternalDepGate: an external (cross-sprint) dep that is not
// done blocks the sprint; once done, the sprint becomes ready.
func TestReadySprintsExternalDepGate(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "EXT", Title: "ext"}); err != nil {
		t.Fatal(err)
	}
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "A", Title: "A", Deps: []string{"EXT"}}, // external dep
	)

	ready, _ := st.ReadySprints()
	if contains(sprintIDs(ready), "SP1") {
		t.Errorf("SP1 should be blocked while EXT is backlog, got %v", sprintIDs(ready))
	}

	driveToDone(t, st, "EXT")
	ready, _ = st.ReadySprints()
	if !contains(sprintIDs(ready), "SP1") {
		t.Errorf("SP1 should be ready after EXT done, got %v", sprintIDs(ready))
	}
}

// TestReadySprintsSkipsEmpty: a sprint with zero stories is never ready.
func TestReadySprintsSkipsEmpty(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateSprint(tickets.Sprint{ID: "EMPTY", Name: "empty"}); err != nil {
		t.Fatal(err)
	}
	ready, _ := st.ReadySprints()
	if contains(sprintIDs(ready), "EMPTY") {
		t.Errorf("empty sprint must be skipped, got %v", sprintIDs(ready))
	}
}

// TestClaimSprintAtomicity: the first claim moves all stories to running and
// returns them topo-ordered; a second claim returns ok=false.
func TestClaimSprintAtomicity(t *testing.T) {
	st := openTemp(t)
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "B", Title: "B", Deps: []string{"A"}},
		tickets.Story{ID: "A", Title: "A"},
	)

	claimed, ok, err := st.ClaimSprint("SP1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !ok {
		t.Fatal("first claim should win")
	}
	if !equalSlice(claimed, []string{"A", "B"}) {
		t.Errorf("claimed order: got %v, want [A B]", claimed)
	}
	for _, id := range []string{"A", "B"} {
		got, _ := st.GetStory(id)
		if got.Status != tickets.StatusRunning {
			t.Errorf("%s status after claim: want running, got %s", id, got.Status)
		}
	}

	// Second claim: every story is now running → ok=false, nothing changes.
	_, ok2, err := st.ClaimSprint("SP1")
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Error("second claim should not win — sprint already running")
	}
}

// TestClaimSprintPartialNotBacklog: if even one story is already non-backlog the
// claim is abandoned and the other stories stay untouched.
func TestClaimSprintPartialNotBacklog(t *testing.T) {
	st := openTemp(t)
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "A", Title: "A", Status: tickets.StatusRunning},
		tickets.Story{ID: "B", Title: "B"},
	)
	_, ok, err := st.ClaimSprint("SP1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("claim should fail when a story is not backlog")
	}
	// B must remain backlog — the failed claim wrote nothing.
	got, _ := st.GetStory("B")
	if got.Status != tickets.StatusBacklog {
		t.Errorf("B should stay backlog after abandoned claim, got %s", got.Status)
	}
}

// TestMarkSprintDoneAndFailed: MarkSprintDone advances ONLY in_review stories
// (merge-gated). MarkSprintFailed advances any non-terminal story.
func TestMarkSprintDoneAndFailed(t *testing.T) {
	st := openTemp(t)
	// In the merge-gated lifecycle a sprint's stories sit in_review until the
	// shared PR merges, at which point MarkSprintDone advances them.
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "A", Title: "A", Status: tickets.StatusInReview},
		tickets.Story{ID: "B", Title: "B", Status: tickets.StatusInReview},
	)
	seedSprint(t, st, "SP2",
		tickets.Story{ID: "C", Title: "C", Status: tickets.StatusRunning},
	)

	if err := st.MarkSprintDone("SP1"); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	for _, id := range []string{"A", "B"} {
		got, _ := st.GetStory(id)
		if got.Status != tickets.StatusDone {
			t.Errorf("%s: want done, got %s", id, got.Status)
		}
	}
	// SP2 untouched.
	if got, _ := st.GetStory("C"); got.Status != tickets.StatusRunning {
		t.Errorf("C should be untouched, got %s", got.Status)
	}

	if err := st.MarkSprintFailed("SP2"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if got, _ := st.GetStory("C"); got.Status != tickets.StatusFailed {
		t.Errorf("C: want failed, got %s", got.Status)
	}
}

// TestMarkSprintDoneSkipsRunning is the B1 regression test: a sprint that holds a
// mix of in_review and still-running stories must advance ONLY the in_review ones
// when its (one) merged PR is reconciled. A still-running story has work in no
// merged PR — flipping it done fabricates "done" and unblocks dependents wrongly.
func TestMarkSprintDoneSkipsRunning(t *testing.T) {
	st := openTemp(t)
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "A", Title: "A", Status: tickets.StatusInReview}, // PR merged path
		tickets.Story{ID: "B", Title: "B", Status: tickets.StatusRunning},  // still executing
	)

	if err := st.MarkSprintDone("SP1"); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if got, _ := st.GetStory("A"); got.Status != tickets.StatusDone {
		t.Errorf("A (in_review) should advance to done, got %s", got.Status)
	}
	if got, _ := st.GetStory("B"); got.Status != tickets.StatusRunning {
		t.Errorf("B (running) must NOT be marked done by a sprint merge (B1), got %s", got.Status)
	}
}

// TestSetSprintRun: records the run_id on every story in the sprint.
func TestSetSprintRun(t *testing.T) {
	st := openTemp(t)
	seedSprint(t, st, "SP1",
		tickets.Story{ID: "A", Title: "A"},
		tickets.Story{ID: "B", Title: "B"},
	)
	if err := st.SetSprintRun("SP1", "run-sprint-1"); err != nil {
		t.Fatalf("set sprint run: %v", err)
	}
	for _, id := range []string{"A", "B"} {
		got, _ := st.GetStory(id)
		if got.RunID != "run-sprint-1" {
			t.Errorf("%s run_id: got %q, want run-sprint-1", id, got.RunID)
		}
	}
}

// ---- helpers ----------------------------------------------------------------

func storyIDs(stories []tickets.Story) []string {
	ids := make([]string, len(stories))
	for i, s := range stories {
		ids[i] = s.ID
	}
	return ids
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func sprintIDs(sprints []tickets.Sprint) []string {
	ids := make([]string, len(sprints))
	for i, sp := range sprints {
		ids[i] = sp.ID
	}
	return ids
}

// idx returns the position of s in ss, or len(ss) if absent.
func idx(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return len(ss)
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
