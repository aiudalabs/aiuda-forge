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
			{Number: 1, State: "closed"}, // → done
			{Number: 2, State: "open"},   // PR listo → in_review
			{Number: 3, State: "open", Assignees: []string{"copilot-swe-agent"}}, // → running
			{Number: 4, State: "open"}, // → backlog (queda)
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

func TestSyncProjectLinksPRByStoryIDMention(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	// PR de sprint SIN "Closes #n" (cazado en vivo) pero que menciona los ids de
	// las stories → debe ligarlas igual. S-05 no está espejada: intocable.
	gh := &fakeGH{
		issues: []github.IssueState{{Number: 1, State: "open"}, {Number: 2, State: "open"}},
		prs: []github.OpenPR{{
			Number: 46, Title: "feat: implement sprint SP1",
			Body: "## S-01 — Schema\nstuff\n## S-02 — Auth\nmore", URL: "https://github.com/o/r/pull/46", Draft: false,
		}},
	}
	p := NewProjector(st, gh)
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"S-01", "S-02"} {
		s, _ := st.GetStory(id)
		if s.Status != tickets.StatusInReview || s.PRURL == "" {
			t.Fatalf("%s = %s pr=%q, want in_review con pr_url", id, s.Status, s.PRURL)
		}
	}
}

func TestSyncProjectSessionPinsRunning(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	// S-02 fue despachada a Copilot (sesión task activa) pero su task aún no asignó
	// el issue ni abrió PR: la proyección NO debe degradarla a backlog (doble-despacho
	// en auto). Solo las sesiones task (…/tasks/<id>, que el barrido puede soltar)
	// anclan — una URL de runs de claude_action NO (ver TestSyncProjectClaudeActionNoAnchorFlap).
	if _, err := st.SyncExternalStatus("S-02", tickets.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStorySession("S-02", "https://github.com/o/r/tasks/ac083fa2-89ad-4a88-a44d-86cadd1cfad8"); err != nil {
		t.Fatal(err)
	}
	p := NewProjector(st, &fakeGH{issues: []github.IssueState{{Number: 2, State: "open"}}})
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStory("S-02"); s.Status != tickets.StatusRunning {
		t.Fatalf("S-02 = %s, want running (sesión task activa ancla)", s.Status)
	}
}

// TestSyncProjectClaudeActionRunningLabel: un issue con el label agent:running (que
// el workflow claude.yml pone mientras corre) deriva running aunque no tenga
// assignee ni PR — la señal de "corriendo" observable en GitHub del canal claude_action.
func TestSyncProjectClaudeActionRunningLabel(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	gh := &fakeGH{issues: []github.IssueState{{Number: 2, State: "open", Labels: []string{"agent:running"}}}}
	p := NewProjector(st, gh)
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStory("S-02"); s.Status != tickets.StatusRunning {
		t.Fatalf("S-02 = %s, want running (label agent:running)", s.Status)
	}
}

// TestSyncProjectClaudeActionNoAnchorFlap es el test de regresión del flap: una
// story de claude_action cuyo run TERMINÓ sin PR (label ya quitado por el paso
// if:always()) debe caer a backlog, NO quedar anclada a running por su sesión
// (página de runs, sin id que el barrido pueda soltar). Antes, esa sesión anclaba
// para siempre → la proyección re-tocaba la story cada tick → flap ready/running.
func TestSyncProjectClaudeActionNoAnchorFlap(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	// Despachada a claude_action: running + sesión = página de runs (sin /tasks/<id>).
	if _, err := st.SyncExternalStatus("S-02", tickets.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStorySession("S-02", "https://github.com/o/r/actions/workflows/claude.yml"); err != nil {
		t.Fatal(err)
	}
	// El run terminó sin PR y sin label (el issue ya no lleva agent:running).
	p := NewProjector(st, &fakeGH{issues: []github.IssueState{{Number: 2, State: "open"}}})
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStory("S-02"); s.Status != tickets.StatusBacklog {
		t.Fatalf("S-02 = %s, want backlog (run claude_action terminado sin PR, sin ancla)", s.Status)
	}
}

type fakeTaskState struct{ state string }

func (f fakeTaskState) AgentTaskState(context.Context, string, string) (string, error) {
	return f.state, nil
}

func TestSyncProjectDeadSessionUnpins(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	if _, err := st.SyncExternalStatus("S-02", tickets.StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStorySession("S-02", "https://github.com/o/r/tasks/ac083fa2-89ad-4a88-a44d-86cadd1cfad8"); err != nil {
		t.Fatal(err)
	}
	p := NewProjector(st, &fakeGH{issues: []github.IssueState{{Number: 2, State: "open"}}})
	p.TaskState = fakeTaskState{state: "failed"}
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}
	// Task muerta → la story vuelve a backlog y su sesión se limpia (queda
	// dispatchable de nuevo, sin resets manuales).
	if s, _ := st.GetStory("S-02"); s.Status != tickets.StatusBacklog {
		t.Fatalf("S-02 = %s, want backlog (sesión muerta)", s.Status)
	}
	urls, _ := st.SessionURLs("p1")
	if urls["S-02"] != "" {
		t.Fatalf("sesión de S-02 debería estar limpia, got %q", urls["S-02"])
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

// fakeFiler stubs the PR file listing; records which PR number was asked.
type fakeFiler struct {
	files []string
	gotPR int
}

func (f *fakeFiler) ListPRFiles(_ context.Context, _ string, n int) ([]string, error) {
	f.gotPR = n
	return f.files, nil
}

func TestSyncProjectCapturesFilesOnMerge(t *testing.T) {
	st := newStore(t)
	if err := st.CreateStory(tickets.Story{ID: "S-01", ProjectID: "p1", Owner: "react-dev", ExternalRef: "github:o/r#1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The story already carried a PR (recorded on a prior in_review pass).
	if _, err := st.SyncExternalStatus("S-01", tickets.StatusInReview, "https://github.com/o/r/pull/45"); err != nil {
		t.Fatalf("pre-set in_review: %v", err)
	}

	filer := &fakeFiler{files: []string{"frontend/src/App.tsx", "frontend/src/Login.tsx"}}
	gh := &fakeGH{issues: []github.IssueState{{Number: 1, State: "closed"}}} // → done
	p := NewProjector(st, gh)
	p.Files = filer
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if filer.gotPR != 45 {
		t.Fatalf("listed PR #%d, want #45 (from pr_url)", filer.gotPR)
	}
	files, err := st.FilesForStory("p1", "S-01")
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	want := []string{"frontend/src/App.tsx", "frontend/src/Login.tsx"}
	if len(files) != len(want) {
		t.Fatalf("captured %v, want %v", files, want)
	}
	// The captured paths must surface in the module map for injection.
	mods, err := st.ModuleMap("p1", 2)
	if err != nil {
		t.Fatalf("modulemap: %v", err)
	}
	if len(mods) != 1 || mods[0].Dir != "frontend/src" || mods[0].Files != 2 {
		t.Fatalf("module map = %+v, want one frontend/src with 2 files", mods)
	}
}

func TestSyncProjectNoFilerNoCaptureNoPanic(t *testing.T) {
	st := newStore(t)
	if err := st.CreateStory(tickets.Story{ID: "S-01", ProjectID: "p1", ExternalRef: "github:o/r#1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gh := &fakeGH{issues: []github.IssueState{{Number: 1, State: "closed"}}}
	p := NewProjector(st, gh) // Files nil — capture must simply not happen
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if files, _ := st.FilesForStory("p1", "S-01"); len(files) != 0 {
		t.Fatalf("captured %v with nil filer, want none", files)
	}
}

func TestSyncProjectFiresOnGraphChanged(t *testing.T) {
	st := newStore(t)
	if err := st.CreateStory(tickets.Story{ID: "S-01", ProjectID: "p1", ExternalRef: "github:o/r#1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := st.SyncExternalStatus("S-01", tickets.StatusInReview, "https://github.com/o/r/pull/45"); err != nil {
		t.Fatalf("pre-set in_review: %v", err)
	}
	fired := 0
	p := NewProjector(st, &fakeGH{issues: []github.IssueState{{Number: 1, State: "closed"}}})
	p.Files = &fakeFiler{files: []string{"frontend/src/App.tsx"}}
	p.OnGraphChanged = func(projectID, repoURL string) { fired++ }
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if fired != 1 {
		t.Fatalf("OnGraphChanged fired %d times, want 1", fired)
	}
	// A second pass captures nothing new → callback must NOT fire again.
	if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if fired != 1 {
		t.Fatalf("OnGraphChanged fired %d times after no-op pass, want 1", fired)
	}
}
