package tickets_test

import (
	"errors"
	"strings"
	"testing"

	"forge/internal/tickets"
)

// mkStory is a small helper: create a backlog story in project p1, failing the test
// on error.
func mkStory(t *testing.T, st *tickets.Store, id, sprint string, deps ...string) {
	t.Helper()
	if err := st.CreateStory(tickets.Story{ID: id, SprintID: sprint, ProjectID: "p1", Title: id, Deps: deps}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func mkSprint(t *testing.T, st *tickets.Store, id string) {
	t.Helper()
	if err := st.CreateSprint(tickets.Sprint{ID: id, ProjectID: "p1", Name: id}); err != nil {
		t.Fatalf("create sprint %s: %v", id, err)
	}
}

func statusOf(t *testing.T, st *tickets.Store, id string) tickets.Status {
	t.Helper()
	got, err := st.GetStoryInProject("p1", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return got.Status
}

// ---- Cancel -----------------------------------------------------------------

func TestCancelStoryLegalSources(t *testing.T) {
	st := openTemp(t)

	// backlog → cancelled
	mkStory(t, st, "C1", "")
	if err := st.CancelStory("p1", "C1"); err != nil {
		t.Fatalf("cancel backlog: %v", err)
	}
	if s := statusOf(t, st, "C1"); s != tickets.StatusCancelled {
		t.Fatalf("C1 status = %s, want cancelled", s)
	}

	// failed → cancelled
	mkStory(t, st, "C2", "")
	if err := st.MarkFailed("C2"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := st.CancelStory("p1", "C2"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if s := statusOf(t, st, "C2"); s != tickets.StatusCancelled {
		t.Fatalf("C2 status = %s, want cancelled", s)
	}

	// in_review → cancelled
	mkStory(t, st, "C3", "")
	if err := st.UpdateStoryStatusInProject("p1", "C3", tickets.StatusRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if err := st.MarkInReviewInProject("p1", "C3", "https://x/pr/1"); err != nil {
		t.Fatalf("→in_review: %v", err)
	}
	if err := st.CancelStory("p1", "C3"); err != nil {
		t.Fatalf("cancel in_review: %v", err)
	}
	if s := statusOf(t, st, "C3"); s != tickets.StatusCancelled {
		t.Fatalf("C3 status = %s, want cancelled", s)
	}
}

func TestCancelRunningRejected(t *testing.T) {
	st := openTemp(t)
	mkStory(t, st, "R1", "")
	if err := st.UpdateStoryStatusInProject("p1", "R1", tickets.StatusRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	err := st.CancelStory("p1", "R1")
	if !errors.Is(err, tickets.ErrIllegalTransition) {
		t.Fatalf("cancel running = %v, want ErrIllegalTransition", err)
	}
	// The story stays running — a live run must never be cancelled from under itself.
	if s := statusOf(t, st, "R1"); s != tickets.StatusRunning {
		t.Fatalf("R1 status = %s, want running (unchanged)", s)
	}
}

func TestCancelDoneAndMissingRejected(t *testing.T) {
	st := openTemp(t)
	// done is terminal — cancel is illegal.
	mkStory(t, st, "D1", "")
	driveToDone(t, st, "D1")
	if err := st.CancelStory("p1", "D1"); !errors.Is(err, tickets.ErrIllegalTransition) {
		t.Fatalf("cancel done = %v, want ErrIllegalTransition", err)
	}
	// missing id → ErrNotFound.
	if err := st.CancelStory("p1", "nope"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("cancel missing = %v, want ErrNotFound", err)
	}
}

// ---- Move -------------------------------------------------------------------

func TestMoveStoryHappyPath(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkSprint(t, st, "SP2")
	mkSprint(t, st, "SP3")
	mkStory(t, st, "A", "SP1")
	mkStory(t, st, "B", "SP2", "A") // B depends on A (SP1)

	// B (SP2) → SP3: still after its dep A(SP1). Allowed.
	if err := st.MoveStory("p1", "B", "SP3"); err != nil {
		t.Fatalf("move B→SP3: %v", err)
	}
	got, _ := st.GetStoryInProject("p1", "B")
	if got.SprintID != "SP3" {
		t.Fatalf("B sprint = %s, want SP3", got.SprintID)
	}
}

func TestMoveBreaksDepOrderRejected(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkSprint(t, st, "SP2")
	mkSprint(t, st, "SP3")
	// A in SP2, B in SP3 depends on A.
	mkStory(t, st, "A", "SP2")
	mkStory(t, st, "B", "SP3", "A")

	// Move B to SP1 (rank 0): earlier than its dependency A (SP2, rank 1) → reject.
	err := st.MoveStory("p1", "B", "SP1")
	if !errors.Is(err, tickets.ErrDepOrder) {
		t.Fatalf("move B→SP1 = %v, want ErrDepOrder", err)
	}
	// B is untouched.
	if got, _ := st.GetStoryInProject("p1", "B"); got.SprintID != "SP3" {
		t.Fatalf("B sprint = %s, want unchanged SP3", got.SprintID)
	}

	// Move A LATER than its dependent B: A(SP2) → SP3 puts it after B(SP3)? B is in SP3
	// (rank 2), moving A to SP3 (rank 2): equal is allowed. Move A "past" B needs a
	// later sprint than B. There is none beyond SP3, so add SP4 to prove the dependent
	// guard fires.
	mkSprint(t, st, "SP4")
	if err := st.MoveStory("p1", "A", "SP4"); !errors.Is(err, tickets.ErrDepOrder) {
		t.Fatalf("move A→SP4 (past dependent B) = %v, want ErrDepOrder", err)
	}
}

func TestMoveOnlyFromBacklogOrFailed(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkSprint(t, st, "SP2")
	mkStory(t, st, "X", "SP1")
	if err := st.UpdateStoryStatusInProject("p1", "X", tickets.StatusRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if err := st.MoveStory("p1", "X", "SP2"); !errors.Is(err, tickets.ErrInvalidState) {
		t.Fatalf("move running = %v, want ErrInvalidState", err)
	}
	// A failed story CAN move.
	mkStory(t, st, "Y", "SP1")
	if err := st.MarkFailed("Y"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := st.MoveStory("p1", "Y", "SP2"); err != nil {
		t.Fatalf("move failed story: %v", err)
	}
}

func TestMoveToUnknownSprintRejected(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkStory(t, st, "Z", "SP1")
	if err := st.MoveStory("p1", "Z", "SP-nope"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("move to unknown sprint = %v, want ErrNotFound", err)
	}
}

// ---- Split ------------------------------------------------------------------

func TestSplitInheritsAndRewires(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateEpic(tickets.Epic{ID: "E1", Title: "epic"}); err != nil {
		t.Fatalf("epic: %v", err)
	}
	mkSprint(t, st, "SP1")
	// D is a dependency of P; C depends on P.
	mkStory(t, st, "D", "SP1")
	if err := st.CreateStory(tickets.Story{ID: "P", ProjectID: "p1", EpicID: "E1", SprintID: "SP1", Title: "parent", Body: "orig", Owner: "backend", Deps: []string{"D"}, Kind: tickets.KindBug}); err != nil {
		t.Fatalf("create P: %v", err)
	}
	mkStory(t, st, "C", "SP1", "P")

	ids, err := st.SplitStory("p1", "P", []tickets.StoryDraft{
		{Title: "part one", Body: "b1"},
		{Title: "part two", Body: "b2", Owner: "frontend"},
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if strings.Join(ids, ",") != "P-a,P-b" {
		t.Fatalf("split ids = %v, want [P-a P-b]", ids)
	}

	// Original is cancelled with the note.
	orig, _ := st.GetStoryInProject("p1", "P")
	if orig.Status != tickets.StatusCancelled {
		t.Fatalf("P status = %s, want cancelled", orig.Status)
	}
	if !strings.Contains(orig.Body, "Split into: P-a, P-b") {
		t.Fatalf("P body missing split note: %q", orig.Body)
	}

	// Parts inherit epic/sprint/kind and the original's deps; owner falls back.
	pa, _ := st.GetStoryInProject("p1", "P-a")
	if pa.EpicID != "E1" || pa.SprintID != "SP1" || pa.Kind != tickets.KindBug {
		t.Fatalf("P-a inherit = epic %q sprint %q kind %q", pa.EpicID, pa.SprintID, pa.Kind)
	}
	if pa.Status != tickets.StatusBacklog {
		t.Fatalf("P-a status = %s, want backlog", pa.Status)
	}
	if strings.Join(pa.Deps, ",") != "D" {
		t.Fatalf("P-a deps = %v, want [D]", pa.Deps)
	}
	if pa.Owner != "backend" {
		t.Fatalf("P-a owner = %q, want inherited 'backend'", pa.Owner)
	}
	pb, _ := st.GetStoryInProject("p1", "P-b")
	if pb.Owner != "frontend" {
		t.Fatalf("P-b owner = %q, want override 'frontend'", pb.Owner)
	}

	// Dependent C is rewired off P and onto both parts (no dangling dep to a cancelled
	// story).
	c, _ := st.GetStoryInProject("p1", "C")
	if strings.Join(c.Deps, ",") != "P-a,P-b" {
		t.Fatalf("C deps = %v, want [P-a P-b] after rewire", c.Deps)
	}
}

func TestSplitGuards(t *testing.T) {
	st := openTemp(t)
	mkStory(t, st, "P", "")

	// Fewer than 2 parts → ErrInvalidInput.
	if _, err := st.SplitStory("p1", "P", []tickets.StoryDraft{{Title: "only"}}); !errors.Is(err, tickets.ErrInvalidInput) {
		t.Fatalf("split 1 part = %v, want ErrInvalidInput", err)
	}
	// Explicit id colliding with an existing story → ErrInvalidInput.
	mkStory(t, st, "EXISTS", "")
	if _, err := st.SplitStory("p1", "P", []tickets.StoryDraft{{ID: "EXISTS", Title: "a"}, {Title: "b"}}); !errors.Is(err, tickets.ErrInvalidInput) {
		t.Fatalf("split colliding id = %v, want ErrInvalidInput", err)
	}
	// Running story cannot be split.
	mkStory(t, st, "RUN", "")
	if err := st.UpdateStoryStatusInProject("p1", "RUN", tickets.StatusRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if _, err := st.SplitStory("p1", "RUN", []tickets.StoryDraft{{Title: "a"}, {Title: "b"}}); !errors.Is(err, tickets.ErrInvalidState) {
		t.Fatalf("split running = %v, want ErrInvalidState", err)
	}
	// After a failed guard the original is untouched (no orphan parts).
	if s := statusOf(t, st, "P"); s != tickets.StatusBacklog {
		t.Fatalf("P status = %s, want backlog (untouched)", s)
	}
}

// ---- Edit -------------------------------------------------------------------

func strptr(s string) *string { return &s }

func TestEditFieldsAndDeps(t *testing.T) {
	st := openTemp(t)
	mkStory(t, st, "D1", "")
	mkStory(t, st, "D2", "")
	if err := st.CreateStory(tickets.Story{ID: "S", ProjectID: "p1", Title: "old", Deps: []string{"D1"}}); err != nil {
		t.Fatalf("create S: %v", err)
	}

	// Field edit.
	if err := st.EditStory("p1", "S", tickets.StoryPatch{Title: strptr("new title"), Owner: strptr("backend")}); err != nil {
		t.Fatalf("edit fields: %v", err)
	}
	got, _ := st.GetStoryInProject("p1", "S")
	if got.Title != "new title" || got.Owner != "backend" {
		t.Fatalf("edit fields = title %q owner %q", got.Title, got.Owner)
	}

	// Dep replacement.
	newDeps := []string{"D2"}
	if err := st.EditStory("p1", "S", tickets.StoryPatch{Deps: &newDeps}); err != nil {
		t.Fatalf("edit deps: %v", err)
	}
	got, _ = st.GetStoryInProject("p1", "S")
	if strings.Join(got.Deps, ",") != "D2" {
		t.Fatalf("deps = %v, want [D2]", got.Deps)
	}

	// Clearing deps with an empty (non-nil) slice.
	empty := []string{}
	if err := st.EditStory("p1", "S", tickets.StoryPatch{Deps: &empty}); err != nil {
		t.Fatalf("clear deps: %v", err)
	}
	got, _ = st.GetStoryInProject("p1", "S")
	if len(got.Deps) != 0 {
		t.Fatalf("deps = %v, want empty", got.Deps)
	}
}

func TestEditDepValidation(t *testing.T) {
	st := openTemp(t)
	mkStory(t, st, "A", "")
	mkStory(t, st, "B", "", "A") // B depends on A

	// Self-dependency rejected.
	self := []string{"A"}
	if err := st.EditStory("p1", "A", tickets.StoryPatch{Deps: &self}); !errors.Is(err, tickets.ErrDepCycle) {
		t.Fatalf("self dep = %v, want ErrDepCycle", err)
	}
	// Non-existent dep rejected.
	ghost := []string{"nope"}
	if err := st.EditStory("p1", "A", tickets.StoryPatch{Deps: &ghost}); !errors.Is(err, tickets.ErrDepNotFound) {
		t.Fatalf("missing dep = %v, want ErrDepNotFound", err)
	}
	// Cycle rejected: A depends on B while B already depends on A.
	cyc := []string{"B"}
	if err := st.EditStory("p1", "A", tickets.StoryPatch{Deps: &cyc}); !errors.Is(err, tickets.ErrDepCycle) {
		t.Fatalf("cycle dep = %v, want ErrDepCycle", err)
	}
	// A's deps stayed empty (validation rejected before any write).
	if got, _ := st.GetStoryInProject("p1", "A"); len(got.Deps) != 0 {
		t.Fatalf("A deps = %v, want empty after rejected edits", got.Deps)
	}
}

func TestEditRejectedOnRunningAndTerminal(t *testing.T) {
	st := openTemp(t)
	mkStory(t, st, "RUN", "")
	if err := st.UpdateStoryStatusInProject("p1", "RUN", tickets.StatusRunning); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if err := st.EditStory("p1", "RUN", tickets.StoryPatch{Title: strptr("x")}); !errors.Is(err, tickets.ErrInvalidState) {
		t.Fatalf("edit running = %v, want ErrInvalidState", err)
	}
	mkStory(t, st, "DONE", "")
	driveToDone(t, st, "DONE")
	if err := st.EditStory("p1", "DONE", tickets.StoryPatch{Title: strptr("x")}); !errors.Is(err, tickets.ErrInvalidState) {
		t.Fatalf("edit done = %v, want ErrInvalidState", err)
	}
}

// ---- Kind -------------------------------------------------------------------

func TestKindDefaultAndValidation(t *testing.T) {
	st := openTemp(t)
	// Default kind is 'story'.
	mkStory(t, st, "K1", "")
	if got, _ := st.GetStoryInProject("p1", "K1"); got.Kind != tickets.KindStory {
		t.Fatalf("default kind = %q, want story", got.Kind)
	}
	// Explicit bug is preserved.
	if err := st.CreateStory(tickets.Story{ID: "K2", ProjectID: "p1", Title: "bug", Kind: tickets.KindBug}); err != nil {
		t.Fatalf("create bug: %v", err)
	}
	if got, _ := st.GetStoryInProject("p1", "K2"); got.Kind != tickets.KindBug {
		t.Fatalf("kind = %q, want bug", got.Kind)
	}
	// An invalid kind is rejected.
	if err := st.CreateStory(tickets.Story{ID: "K3", ProjectID: "p1", Title: "x", Kind: "epic"}); err == nil {
		t.Fatalf("invalid kind should be rejected")
	}
}
