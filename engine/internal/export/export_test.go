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
	got := PriorArtForLane("react-dev", mods)
	if !strings.Contains(got, "frontend/src/") || strings.Contains(got, "backend/api/") {
		t.Fatalf("react-dev prior art = %q, want only its own module", got)
	}
	if !strings.Contains(got, "S1, S4") {
		t.Fatalf("prior art should cite the stories: %q", got)
	}
	// A lane with no prior work → empty, so a fresh export is unchanged.
	if s := PriorArtForLane("flutter-dev", mods); s != "" {
		t.Fatalf("unknown lane prior art = %q, want empty", s)
	}
	if s := PriorArtForLane("react-dev", nil); s != "" {
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

// A zero Enrichment must reproduce the plain export body byte-for-byte — the
// groom-off invariant (VIBEFORGE_CONDUCTOR_GROOM=0). issueBody itself delegates to
// EnrichedBody with a zero Enrichment, so this pins the two together.
func TestEnrichedBodyEmptyIsByteIdentical(t *testing.T) {
	st := tickets.Story{ID: "S1", SprintID: "SP1", Owner: "react-dev", Body: "As a user…", Accept: "- a\n- b"}
	for _, priorArt := range []string{"", "### Prior art (x)\n- `y/`\n\n"} {
		if got, want := EnrichedBody(st, priorArt, Enrichment{}), issueBody(st, priorArt); got != want {
			t.Fatalf("EnrichedBody(zero) != issueBody:\n--- got ---\n%s\n--- want ---\n%s", got, want)
		}
	}
}

// With a screen_key the enriched body carries all three sections; without one the
// visual section is absent (backend stories never get a mockup).
func TestEnrichedBodySections(t *testing.T) {
	st := tickets.Story{ID: "S1", Owner: "react-dev", Body: "As a user…", Accept: "- works"}

	enrich := Enrichment{
		SpecDevReady: SpecSection("Build the LoginForm component."),
		VisualSpec:   VisualSpecSection("customer.login", "https://raw.example/docs/mockups/customer.login.html", "### Login\nEmail + password."),
		Components:   ComponentsSection([]string{"src/components/Button.tsx"}),
	}
	body := EnrichedBody(st, "", enrich)
	for _, want := range []string{
		"## Spec (dev-ready)", "Build the LoginForm component.",
		"## Visual spec", "customer.login", "raw.example/docs/mockups/customer.login.html",
		"el mockup manda sobre tu criterio visual",
		"## Componentes existentes", "src/components/Button.tsx", "REUSA antes de crear",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enriched body missing %q:\n%s", want, body)
		}
	}

	// No screen_key → no visual section, but spec + components still render.
	noScreen := Enrichment{
		SpecDevReady: SpecSection("Add the /login endpoint."),
		VisualSpec:   VisualSpecSection("", "", ""),
		Components:   ComponentsSection([]string{"src/components/Button.tsx"}),
	}
	body = EnrichedBody(st, "", noScreen)
	if strings.Contains(body, "## Visual spec") {
		t.Fatalf("visual section leaked without a screen_key:\n%s", body)
	}
	if !strings.Contains(body, "## Spec (dev-ready)") || !strings.Contains(body, "## Componentes existentes") {
		t.Fatalf("spec/components dropped when visual absent:\n%s", body)
	}
}

// TestHasScreenAndNoneMarker: "none" is the explicit frontend-foundation marker — a
// story with no screen of its own — and must be treated identically to an empty key
// (no real screen), so a foundation story never gets a Visual spec (and the art-director
// QA that reads that section from the issue therefore skips it cleanly).
func TestHasScreenAndNoneMarker(t *testing.T) {
	if !HasScreen("customer.login") {
		t.Error("a real key must count as a screen")
	}
	for _, k := range []string{"", "  ", "none", "None", "NONE"} {
		if HasScreen(k) {
			t.Errorf("HasScreen(%q) = true, want false (absent or the none marker)", k)
		}
	}
	// "none" produces no Visual spec, exactly like an empty key.
	if got := VisualSpecSection("none", "https://raw.example/docs/mockups/none.html", "### X"); got != "" {
		t.Errorf("VisualSpecSection(none) must render nothing, got %q", got)
	}
}

// An empty spec (detailer failed) drops only the Spec section — the degrade path.
func TestSpecSectionDegrades(t *testing.T) {
	if s := SpecSection("   "); s != "" {
		t.Fatalf("empty spec should render nothing, got %q", s)
	}
	if s := ComponentsSection(nil); s != "" {
		t.Fatalf("no components should render nothing, got %q", s)
	}
}
