package conductor

import (
	"context"
	"path/filepath"
	"testing"

	"forge/internal/github"
	"forge/internal/tickets"
)

func newStore(t *testing.T) *tickets.Store {
	t.Helper()
	st, err := tickets.Open(filepath.Join(t.TempDir(), "tickets.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

type fakeGH struct {
	issues []github.IssueState
	prs    []github.OpenPR
}

func (f *fakeGH) ListIssueStates(context.Context, string) ([]github.IssueState, error) {
	return f.issues, nil
}
func (f *fakeGH) ListOpenPRs(context.Context, string) ([]github.OpenPR, error) {
	return f.prs, nil
}

func seed(t *testing.T, st *tickets.Store) {
	t.Helper()
	stories := []tickets.Story{
		{ID: "S-01", Title: "a", ProjectID: "p1", ExternalRef: "github:o/r#1"},
		{ID: "S-02", Title: "b", ProjectID: "p1", ExternalRef: "github:o/r#2"},
		{ID: "S-03", Title: "c", ProjectID: "p1", ExternalRef: "github:o/r#3"},
		{ID: "S-04", Title: "d", ProjectID: "p1", ExternalRef: "github:o/r#4"},
		{ID: "S-05", Title: "local only", ProjectID: "p1"}, // sin espejo — intocable
	}
	for _, s := range stories {
		if err := st.CreateStory(s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}
}

func TestSyncProjectDerivesStatuses(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	gh := &fakeGH{
		issues: []github.IssueState{
			{Number: 1, State: "closed"},                                          // → done
			{Number: 2, State: "open"},                                            // PR listo → in_review
			{Number: 3, State: "open", Assignees: []string{"copilot-swe-agent"}},  // → running
			{Number: 4, State: "open"},                                            // → backlog (queda)
		},
		prs: []github.OpenPR{
			{Number: 45, Body: "Implements auth.\n\nCloses #2", URL: "https://github.com/o/r/pull/45", Draft: false},
		},
	}
	p := NewProjector(st, gh)
	res, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Mirrored != 4 || res.Changed != 3 {
		t.Fatalf("mirrored/changed = %d/%d, want 4/3", res.Mirrored, res.Changed)
	}
	want := map[string]tickets.Status{
		"S-01": tickets.StatusDone,
		"S-02": tickets.StatusInReview,
		"S-03": tickets.StatusRunning,
		"S-04": tickets.StatusBacklog,
		"S-05": tickets.StatusBacklog, // no espejada: jamás tocada
	}
	for id, wantSt := range want {
		got, err := st.GetStory(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != wantSt {
			t.Errorf("%s = %s, want %s", id, got.Status, wantSt)
		}
	}
	s2, _ := st.GetStory("S-02")
	if s2.PRURL != "https://github.com/o/r/pull/45" {
		t.Errorf("S-02 pr_url = %q", s2.PRURL)
	}
}

func TestSyncProjectDraftPRMeansRunning(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	gh := &fakeGH{
		issues: []github.IssueState{{Number: 2, State: "open"}},
		prs:    []github.OpenPR{{Number: 45, Body: "WIP\n\nFixes #2", URL: "u", Draft: true}},
	}
	p := NewProjector(st, gh)
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	s2, _ := st.GetStory("S-02")
	if s2.Status != tickets.StatusRunning {
		t.Fatalf("S-02 = %s, want running (draft PR)", s2.Status)
	}
}

func TestSyncProjectReopenRollsBack(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	p := NewProjector(st, &fakeGH{issues: []github.IssueState{{Number: 1, State: "closed"}}})
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStory("S-01"); s.Status != tickets.StatusDone {
		t.Fatalf("precondición: S-01 done, got %s", s.Status)
	}
	// El issue se reabre → GitHub manda: la story vuelve a backlog aunque la
	// tabla legal del kernel jamás permitiría done→backlog.
	p2 := NewProjector(st, &fakeGH{issues: []github.IssueState{{Number: 1, State: "open"}}})
	if _, err := p2.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStory("S-01"); s.Status != tickets.StatusBacklog {
		t.Fatalf("S-01 tras reopen = %s, want backlog", s.Status)
	}
}

func TestSyncProjectSessionPinsRunning(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	// S-02 fue despachada (sesión activa) pero su task aún no asignó el issue ni
	// abrió PR: la proyección NO debe degradarla a backlog (doble-despacho en auto).
	if _, err := st.SyncExternalStatus("S-02", tickets.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStorySession("S-02", "https://github.com/o/r/tasks/t1"); err != nil {
		t.Fatal(err)
	}
	p := NewProjector(st, &fakeGH{issues: []github.IssueState{{Number: 2, State: "open"}}})
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStory("S-02"); s.Status != tickets.StatusRunning {
		t.Fatalf("S-02 = %s, want running (sesión activa ancla)", s.Status)
	}
}

func TestClosesRefs(t *testing.T) {
	body := "Does stuff.\n\nCloses #7, fixes #12; Resolved #3. See #99 (unrelated)."
	got := closesRefs(body)
	want := []int{7, 12, 3}
	if len(got) != len(want) {
		t.Fatalf("refs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("refs = %v, want %v", got, want)
		}
	}
}
