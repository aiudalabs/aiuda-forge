package conductor

import (
	"context"
	"strings"
	"testing"
	"time"

	"forge/internal/agent"
	"forge/internal/github"
	"forge/internal/tickets"
)

// fakeGroomGH is the GroomGitHub seam: canned docs + repo listings, recorded PATCHes.
type fakeGroomGH struct {
	files    map[string]string           // "docs/PRD.md" → content
	contents map[string][]github.DocEntry // "src/components" → entries
	patched  map[int]string              // issue number → new body
}

func newFakeGroomGH() *fakeGroomGH {
	return &fakeGroomGH{files: map[string]string{}, contents: map[string][]github.DocEntry{}, patched: map[int]string{}}
}

func (f *fakeGroomGH) ReadFile(_ context.Context, _, path, _ string) (string, error) {
	if v, ok := f.files[path]; ok {
		return v, nil
	}
	return "", errNotFound
}

func (f *fakeGroomGH) ListContents(_ context.Context, _, dir, _ string) ([]github.DocEntry, error) {
	if v, ok := f.contents[dir]; ok {
		return v, nil
	}
	return nil, errNotFound
}

func (f *fakeGroomGH) UpdateIssueBody(_ context.Context, _ string, number int, body string) error {
	if f.patched == nil {
		f.patched = map[int]string{}
	}
	f.patched[number] = body
	return nil
}

var errNotFound = errNF("not found")

type errNF string

func (e errNF) Error() string { return string(e) }

func detailerLoader() agent.Loader {
	return agent.MapLoader{groomAgentID: {ID: groomAgentID, Model: "m", Tools: []string{"read"}, Persona: "persona"}}
}

// Happy path: a frontend story (screen_key present) gets all three sections.
func TestGroomIssueAllSections(t *testing.T) {
	fake := newFakeGroomGH()
	fake.files["docs/UI_SCREENS.md"] = "# Customer app\n\n## customer.login\nEmail + password form.\n\n## customer.home\nHome feed.\n"
	fake.contents["src/components"] = []github.DocEntry{
		{Name: "Button.tsx", Path: "src/components/Button.tsx", Type: "file"},
		{Name: "ui", Path: "src/components/ui", Type: "dir"},
	}
	fake.contents["src/components/ui"] = []github.DocEntry{
		{Name: "Input.tsx", Path: "src/components/ui/Input.tsx", Type: "file"},
	}

	g := &Groomer{Backend: agent.FakeBackend{Reply: "DEV-READY SPEC: build LoginForm."}, Agents: detailerLoader(), Timeout: time.Minute}
	st := tickets.Story{ID: "S1", Owner: "react-dev", Body: "As a user…", Accept: "- login works"}

	if err := g.GroomIssue(context.Background(), fake, "https://github.com/o/r", st, 7, "customer.login", ""); err != nil {
		t.Fatalf("GroomIssue: %v", err)
	}
	body, ok := fake.patched[7]
	if !ok {
		t.Fatal("issue #7 was not patched")
	}
	for _, want := range []string{
		"## Spec (dev-ready)", "DEV-READY SPEC: build LoginForm.",
		"## Visual spec", "customer.login",
		"https://raw.githubusercontent.com/o/r/main/docs/mockups/customer.login.html",
		"Email + password form.", "el mockup manda sobre tu criterio visual",
		"## Componentes existentes", "src/components/Button.tsx", "src/components/ui/Input.tsx", "REUSA antes de crear",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("patched body missing %q:\n%s", want, body)
		}
	}
	// The home section must NOT leak into the login extract.
	if strings.Contains(body, "Home feed.") {
		t.Fatalf("UI extract bled into the next screen:\n%s", body)
	}
}

// No screen_key (backend story) → no visual section; spec still lands.
func TestGroomIssueNoScreenKeyNoVisual(t *testing.T) {
	fake := newFakeGroomGH()
	g := &Groomer{Backend: agent.FakeBackend{Reply: "DEV-READY SPEC: add /login endpoint."}, Agents: detailerLoader(), Timeout: time.Minute}
	st := tickets.Story{ID: "S2", Owner: "python-dev", Body: "As an API…", Accept: "- endpoint works"}

	if err := g.GroomIssue(context.Background(), fake, "https://github.com/o/r", st, 9, "", ""); err != nil {
		t.Fatalf("GroomIssue: %v", err)
	}
	body := fake.patched[9]
	if body == "" {
		t.Fatal("issue #9 not patched (spec should still enrich)")
	}
	if strings.Contains(body, "## Visual spec") {
		t.Fatalf("visual section present without screen_key:\n%s", body)
	}
	if !strings.Contains(body, "## Spec (dev-ready)") {
		t.Fatalf("spec section missing:\n%s", body)
	}
	// python-dev has no component convention → no components section.
	if strings.Contains(body, "## Componentes existentes") {
		t.Fatalf("components section for a backend lane:\n%s", body)
	}
}

// Detailer failure/timeout degrades — the Spec section drops but the dispatch must
// not be blocked (GroomIssue returns nil) and the other sections still land.
func TestGroomIssueDetailerFailureDegrades(t *testing.T) {
	fake := newFakeGroomGH()
	fake.contents["src/components"] = []github.DocEntry{{Name: "Card.tsx", Path: "src/components/Card.tsx", Type: "file"}}

	g := &Groomer{Backend: agent.FakeBackend{Fail: true}, Agents: detailerLoader(), Timeout: time.Minute}
	st := tickets.Story{ID: "S3", Owner: "react-dev", Body: "As a user…", Accept: "- works"}

	if err := g.GroomIssue(context.Background(), fake, "https://github.com/o/r", st, 11, "customer.login", ""); err != nil {
		t.Fatalf("detailer failure must not error the groom: %v", err)
	}
	body := fake.patched[11]
	if strings.Contains(body, "## Spec (dev-ready)") {
		t.Fatalf("failed detailer should drop the Spec section:\n%s", body)
	}
	if !strings.Contains(body, "## Visual spec") || !strings.Contains(body, "## Componentes existentes") {
		t.Fatalf("degrade should keep visual+components:\n%s", body)
	}
}

// Nothing to add (backend story, detailer failed, no components) → no PATCH at all.
func TestGroomIssueNothingToAddSkipsPatch(t *testing.T) {
	fake := newFakeGroomGH()
	g := &Groomer{Backend: agent.FakeBackend{Fail: true}, Agents: detailerLoader(), Timeout: time.Minute}
	st := tickets.Story{ID: "S4", Owner: "python-dev", Body: "As an API…", Accept: "- works"}

	if err := g.GroomIssue(context.Background(), fake, "https://github.com/o/r", st, 13, "", ""); err != nil {
		t.Fatalf("GroomIssue: %v", err)
	}
	if _, ok := fake.patched[13]; ok {
		t.Fatalf("issue should not be patched when there is nothing to enrich")
	}
}

// screen_key comes from docs/backlog.yaml (the store does not persist it).
func TestScreenKeysFromBacklog(t *testing.T) {
	fake := newFakeGroomGH()
	fake.files["docs/backlog.yaml"] = `stories:
  - id: S1
    title: Login
    screen_key: customer.login
  - id: S2
    title: Auth endpoint
`
	g := &Groomer{}
	keys := g.screenKeys(context.Background(), fake, "https://github.com/o/r")
	if keys["S1"] != "customer.login" {
		t.Fatalf("S1 screen_key = %q, want customer.login", keys["S1"])
	}
	if _, ok := keys["S2"]; ok {
		t.Fatalf("S2 has no screen_key, must be absent: %v", keys)
	}
}

// GroomStories is the dispatch entry point: it resolves screen_key from the
// backlog, loads each story, and patches its issue. Also the groom-off invariant:
// a nil Groomer patches nothing.
func TestGroomStoriesEndToEnd(t *testing.T) {
	st := newStore(t)
	story := tickets.Story{ID: "S1", Title: "Login UI", Owner: "react-dev", Body: "As a user…", Accept: "- works",
		ProjectID: "p1", ExternalRef: "github:o/r#42"}
	if err := st.CreateStory(story); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fake := newFakeGroomGH()
	fake.files["docs/backlog.yaml"] = "stories:\n  - id: S1\n    screen_key: customer.login\n"
	fake.contents["src/components"] = []github.DocEntry{{Name: "Button.tsx", Path: "src/components/Button.tsx", Type: "file"}}

	g := &Groomer{
		Backend:   agent.FakeBackend{Reply: "DEV-READY SPEC."},
		Agents:    detailerLoader(),
		Tickets:   st,
		Timeout:   time.Minute,
		ClientFor: func(context.Context, string) GroomGitHub { return fake },
	}
	g.GroomStories(context.Background(), "p1", "https://github.com/o/r", []string{"S1"})

	body, ok := fake.patched[42]
	if !ok {
		t.Fatal("story issue #42 not enriched")
	}
	if !strings.Contains(body, "## Visual spec") || !strings.Contains(body, "customer.login") {
		t.Fatalf("screen_key from backlog not applied:\n%s", body)
	}

	// Groom-off: a nil Groomer is a no-op (nil-receiver safe) and patches nothing.
	fresh := newFakeGroomGH()
	var off *Groomer
	off.GroomStories(context.Background(), "p1", "https://github.com/o/r", []string{"S1"})
	if len(fresh.patched) != 0 {
		t.Fatalf("nil groomer must not patch")
	}
}

// The store is the source of truth for screen_key: when a story carries one, the
// groom uses it and NEVER falls back to the (possibly stale) docs/backlog.yaml. The
// backlog.yaml here says a different screen on purpose — a mid-sprint move would have
// left it stale, and the groom must ignore it.
func TestGroomStoriesPrefersStoreScreenKey(t *testing.T) {
	st := newStore(t)
	story := tickets.Story{ID: "S1", Title: "Dashboard UI", Owner: "react-dev", Body: "As a user…", Accept: "- works",
		ProjectID: "p1", ExternalRef: "github:o/r#42", ScreenKey: "customer.dashboard"}
	if err := st.CreateStory(story); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fake := newFakeGroomGH()
	// Stale snapshot: the YAML still points at the OLD screen. The store must win.
	fake.files["docs/backlog.yaml"] = "stories:\n  - id: S1\n    screen_key: customer.login\n"

	g := &Groomer{
		Backend:   agent.FakeBackend{Reply: "DEV-READY SPEC."},
		Agents:    detailerLoader(),
		Tickets:   st,
		Timeout:   time.Minute,
		ClientFor: func(context.Context, string) GroomGitHub { return fake },
	}
	g.GroomStories(context.Background(), "p1", "https://github.com/o/r", []string{"S1"})

	body, ok := fake.patched[42]
	if !ok {
		t.Fatal("story issue #42 not enriched")
	}
	if !strings.Contains(body, "docs/mockups/customer.dashboard.html") {
		t.Fatalf("store screen_key not used:\n%s", body)
	}
	if strings.Contains(body, "customer.login") {
		t.Fatalf("stale backlog.yaml screen_key leaked past the store:\n%s", body)
	}
}

func TestExtractHeadingSection(t *testing.T) {
	md := "# App\n\n## customer.catalog\nList of products.\n\n### sub\ndetail\n\n## customer.cart\nCart.\n"
	got := extractHeadingSection(md, "customer.catalog")
	if !strings.Contains(got, "List of products.") || !strings.Contains(got, "detail") {
		t.Fatalf("extract missing content: %q", got)
	}
	if strings.Contains(got, "Cart.") {
		t.Fatalf("extract bled past the section: %q", got)
	}
	// Fuzzy: last segment matches when the exact key doesn't.
	if got := extractHeadingSection("## Catalog screen\nbody\n", "customer.catalog"); !strings.Contains(got, "body") {
		t.Fatalf("last-segment fuzzy match failed: %q", got)
	}
	if got := extractHeadingSection("## other\nx\n", "customer.catalog"); got != "" {
		t.Fatalf("no match should be empty, got %q", got)
	}
}

func TestLaneComponentDir(t *testing.T) {
	cases := map[string]string{"flutter-dev": "lib/widgets", "react-dev": "src/components", "python-dev": "", "": ""}
	for owner, want := range cases {
		if got := laneComponentDir(owner); got != want {
			t.Fatalf("laneComponentDir(%q) = %q, want %q", owner, got, want)
		}
	}
}
