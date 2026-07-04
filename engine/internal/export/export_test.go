package export

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/github"
	"forge/internal/tickets"
)

func init() { writeDelay = 0 } // no pacing in tests

func newStore(t *testing.T) *tickets.Store {
	t.Helper()
	st, err := tickets.Open(filepath.Join(t.TempDir(), "tickets.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fakeGH records writes and hands out deterministic numbers/ids.
type fakeGH struct {
	labels   map[string]bool
	issues   []string // created titles, in order
	deps     []string // "target<-blockerID"
	nextNum  int
	failOnce string // issue title that fails once (mid-run failure simulation)
}

func newFakeGH() *fakeGH { return &fakeGH{labels: map[string]bool{}, nextNum: 100} }

func (f *fakeGH) EnsureLabel(_ context.Context, _, name, _ string) (bool, error) {
	if f.labels[name] {
		return false, nil
	}
	f.labels[name] = true
	return true, nil
}

func (f *fakeGH) CreateIssue(_ context.Context, _, title, body string, _ []string) (github.CreatedIssue, error) {
	if f.failOnce != "" && strings.HasPrefix(title, f.failOnce) {
		f.failOnce = ""
		return github.CreatedIssue{}, fmt.Errorf("boom")
	}
	f.nextNum++
	f.issues = append(f.issues, title)
	return github.CreatedIssue{Number: f.nextNum, ID: int64(f.nextNum) * 10}, nil
}

func (f *fakeGH) IssueID(_ context.Context, _ string, number int) (int64, error) {
	return int64(number) * 10, nil
}

func (f *fakeGH) AddIssueBlockedBy(_ context.Context, _ string, issueNumber int, blockedByID int64) (bool, error) {
	edge := fmt.Sprintf("%d<-%d", issueNumber, blockedByID)
	for _, d := range f.deps {
		if d == edge {
			return false, nil
		}
	}
	f.deps = append(f.deps, edge)
	return true, nil
}

func seed(t *testing.T, st *tickets.Store) {
	t.Helper()
	stories := []tickets.Story{
		{ID: "S1-01", Title: "Schema", Body: "As an operator…", Accept: "- migrations run\n- idempotent", SprintID: "SP1", EpicID: "E1", Owner: "python-dev", ProjectID: "p1"},
		{ID: "S1-02", Title: "Auth", SprintID: "SP1", Owner: "python-dev", ProjectID: "p1"},
		{ID: "S1-07", Title: "Login API", SprintID: "SP2", Owner: "python-dev", ProjectID: "p1", Deps: []string{"S1-01", "S1-02"}},
	}
	for _, s := range stories {
		if err := st.CreateStory(s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}
}

func TestExportCreatesIssuesLabelsAndDeps(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	gh := newFakeGH()

	res, err := GitHubBacklog(context.Background(), st, gh, "p1", "https://github.com/o/r")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.IssuesCreated != 3 || res.IssuesSkipped != 0 {
		t.Fatalf("issues created/skipped = %d/%d, want 3/0", res.IssuesCreated, res.IssuesSkipped)
	}
	// SP1, SP2, lane:python-dev, E1
	if res.LabelsCreated != 4 {
		t.Fatalf("labels = %d, want 4", res.LabelsCreated)
	}
	if res.DepsCreated != 2 {
		t.Fatalf("deps = %d, want 2", res.DepsCreated)
	}
	// external_ref recorded → story linked to its issue number
	story, err := st.GetStory("S1-01")
	if err != nil {
		t.Fatal(err)
	}
	if story.ExternalRef != "github:o/r#101" {
		t.Fatalf("external_ref = %q, want github:o/r#101", story.ExternalRef)
	}
	// deps wired against the BLOCKER's database id (number*10 in the fake)
	want := []string{"103<-1010", "103<-1020"}
	if fmt.Sprint(gh.deps) != fmt.Sprint(want) {
		t.Fatalf("dep edges = %v, want %v", gh.deps, want)
	}
}

func TestExportIsIdempotent(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	gh := newFakeGH()
	if _, err := GitHubBacklog(context.Background(), st, gh, "p1", "https://github.com/o/r"); err != nil {
		t.Fatalf("first export: %v", err)
	}
	res, err := GitHubBacklog(context.Background(), st, gh, "p1", "https://github.com/o/r")
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if res.IssuesCreated != 0 || res.IssuesSkipped != 3 {
		t.Fatalf("second run created/skipped = %d/%d, want 0/3", res.IssuesCreated, res.IssuesSkipped)
	}
	if res.DepsCreated != 0 || res.DepsSkipped != 2 {
		t.Fatalf("second run deps created/skipped = %d/%d, want 0/2", res.DepsCreated, res.DepsSkipped)
	}
	if len(gh.issues) != 3 {
		t.Fatalf("issues on GitHub = %d, want 3 (no duplicates)", len(gh.issues))
	}
}

func TestExportResumesAfterMidRunFailure(t *testing.T) {
	st := newStore(t)
	seed(t, st)
	gh := newFakeGH()
	gh.failOnce = "S1-02" // second issue blows up

	if _, err := GitHubBacklog(context.Background(), st, gh, "p1", "https://github.com/o/r"); err == nil {
		t.Fatal("first export should fail")
	}
	// retry continues: S1-01 skipped (already exported), S1-02/S1-07 created
	res, err := GitHubBacklog(context.Background(), st, gh, "p1", "https://github.com/o/r")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if res.IssuesCreated != 2 || res.IssuesSkipped != 1 {
		t.Fatalf("retry created/skipped = %d/%d, want 2/1", res.IssuesCreated, res.IssuesSkipped)
	}
	if res.DepsCreated != 2 {
		t.Fatalf("retry deps = %d, want 2", res.DepsCreated)
	}
	if len(gh.issues) != 3 {
		t.Fatalf("issues on GitHub = %d, want 3", len(gh.issues))
	}
}

func TestPriorArtForLane(t *testing.T) {
	mods := []tickets.ModuleHit{
		{Dir: "frontend/src", Files: 3, Lanes: []string{"react-dev"}, Stories: []string{"S1", "S4"}},
		{Dir: "backend/api", Files: 2, Lanes: []string{"python-dev"}, Stories: []string{"S2"}},
	}
	got := priorArtForLane("react-dev", mods)
	if !strings.Contains(got, "frontend/src/") || strings.Contains(got, "backend/api/") {
		t.Fatalf("react-dev prior art = %q, want only its own module", got)
	}
	if !strings.Contains(got, "S1, S4") {
		t.Fatalf("prior art should cite the stories: %q", got)
	}
	// A lane with no prior work → empty, so a fresh export is unchanged.
	if s := priorArtForLane("flutter-dev", mods); s != "" {
		t.Fatalf("unknown lane prior art = %q, want empty", s)
	}
	if s := priorArtForLane("react-dev", nil); s != "" {
		t.Fatalf("no modules = %q, want empty", s)
	}
}

func TestIssueBodyPriorArtGating(t *testing.T) {
	st := tickets.Story{ID: "S1", Body: "As a user…", Accept: "- works"}
	if body := issueBody(st, ""); strings.Contains(body, "Prior art") {
		t.Fatalf("empty prior art leaked into body:\n%s", body)
	}
	if body := issueBody(st, "### Prior art (x)\n- `y/`\n\n"); !strings.Contains(body, "Prior art") {
		t.Fatalf("prior art not included:\n%s", body)
	}
}
