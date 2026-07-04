package tickets_test

import (
	"errors"
	"testing"

	"forge/internal/tickets"
)

// GetStoryInProject resolves the row of the RIGHT project when two projects hold a
// story with the same id (the S11-01 cross-tenant incident) — GetStory (unscoped)
// may return either.
func TestGetStoryInProjectDisambiguatesDuplicateIDs(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "S11-01", Title: "orphan", ProjectID: "default"}); err != nil {
		t.Fatalf("seed default: %v", err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S11-01", Title: "real", ProjectID: "p1", Repo: "https://github.com/o/r"}); err != nil {
		t.Fatalf("seed p1: %v", err)
	}

	got, err := st.GetStoryInProject("p1", "S11-01")
	if err != nil {
		t.Fatalf("GetStoryInProject: %v", err)
	}
	if got.ProjectID != "p1" || got.Repo != "https://github.com/o/r" {
		t.Fatalf("got project=%q repo=%q, want p1 with repo", got.ProjectID, got.Repo)
	}

	other, err := st.GetStoryInProject("default", "S11-01")
	if err != nil {
		t.Fatalf("GetStoryInProject default: %v", err)
	}
	if other.ProjectID != "default" || other.Repo != "" {
		t.Fatalf("got project=%q repo=%q, want default with no repo", other.ProjectID, other.Repo)
	}
}

// A guarded status flip scoped to a project only touches THAT project's row, never
// a same-id story in another project (a bare `WHERE id=?` UPDATE would hit both).
func TestUpdateStoryStatusInProjectScoped(t *testing.T) {
	st := openTemp(t)
	if err := st.CreateStory(tickets.Story{ID: "S1-01", ProjectID: "a"}); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S1-01", ProjectID: "b"}); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if err := st.UpdateStoryStatusInProject("a", "S1-01", tickets.StatusRunning); err != nil {
		t.Fatalf("flip a: %v", err)
	}
	a, _ := st.GetStoryInProject("a", "S1-01")
	b, _ := st.GetStoryInProject("b", "S1-01")
	if a.Status != tickets.StatusRunning {
		t.Fatalf("a status = %q, want running", a.Status)
	}
	if b.Status != tickets.StatusBacklog {
		t.Fatalf("b status = %q, want backlog (untouched)", b.Status)
	}
}

// DeleteStory removes the story of the CORRECT project (with duplicate ids), plus
// its dep edges (both directions) and file links, leaving the other project intact.
func TestDeleteStoryScopedRemovesDepsAndFiles(t *testing.T) {
	st := openTemp(t)
	// project p1: S1-01 (dep of S1-02) with a file link; S1-02 depends on S1-01.
	if err := st.CreateStory(tickets.Story{ID: "S1-01", ProjectID: "p1"}); err != nil {
		t.Fatalf("seed p1/S1-01: %v", err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S1-02", ProjectID: "p1", Deps: []string{"S1-01"}}); err != nil {
		t.Fatalf("seed p1/S1-02: %v", err)
	}
	if _, err := st.RecordStoryFiles("p1", "S1-01", []string{"src/a.go"}); err != nil {
		t.Fatalf("record files: %v", err)
	}
	// A same-id story in another project must survive the delete.
	if err := st.CreateStory(tickets.Story{ID: "S1-01", ProjectID: "p2"}); err != nil {
		t.Fatalf("seed p2/S1-01: %v", err)
	}

	// S1-01 has a dependent (S1-02) → dependents must list it.
	deps, err := st.StoryDependents("p1", "S1-01")
	if err != nil {
		t.Fatalf("dependents: %v", err)
	}
	if len(deps) != 1 || deps[0] != "S1-02" {
		t.Fatalf("dependents = %v, want [S1-02]", deps)
	}

	// Remove S1-02 first (it depended on S1-01), then S1-01 deletes cleanly.
	if err := st.DeleteStory("p1", "S1-02"); err != nil {
		t.Fatalf("delete S1-02: %v", err)
	}
	if err := st.DeleteStory("p1", "S1-01"); err != nil {
		t.Fatalf("delete S1-01: %v", err)
	}

	if _, err := st.GetStoryInProject("p1", "S1-01"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("p1/S1-01 still present: %v", err)
	}
	// The p2 same-id story is untouched.
	if _, err := st.GetStoryInProject("p2", "S1-01"); err != nil {
		t.Fatalf("p2/S1-01 was wrongly deleted: %v", err)
	}
	// The dep edge and file link are gone.
	if deps, _ := st.StoryDependents("p1", "S1-01"); len(deps) != 0 {
		t.Fatalf("dep edges survived: %v", deps)
	}
	if mods, _ := st.ModuleMap("p1", 2); len(mods) != 0 {
		t.Fatalf("file links survived: %v", mods)
	}
}

// Deleting a missing story is ErrNotFound.
func TestDeleteStoryNotFound(t *testing.T) {
	st := openTemp(t)
	if err := st.DeleteStory("p1", "ghost"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("delete ghost = %v, want ErrNotFound", err)
	}
}
