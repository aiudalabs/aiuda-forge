package tickets_test

import (
	"context"
	"path/filepath"
	"testing"

	"vibeforge-kernel/internal/tickets"
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

	// Mark D1 done.
	if err := st.UpdateStoryStatus("D1", tickets.StatusDone); err != nil {
		t.Fatalf("update status: %v", err)
	}

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

	if err := p.UpdateStatus(ctx, "S1", tickets.StatusDone); err != nil {
		t.Fatalf("update: %v", err)
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

