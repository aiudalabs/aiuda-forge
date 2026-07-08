package tickets_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/tickets"
	"forge/internal/workflow"
)

// fixture is the canonical backlog.yaml the scrum-master persona produces.
const fixture = `
epic:
  id: E1
  title: "Foundation"
  description: "Core infrastructure stories"
stories:
  - id: S1-01
    title: "Auth service"
    body: "Implement the authentication service with JWT."
    acceptance: "Users can log in and receive a token."
    owner: dev
    sprint_id: SP1
    deps: []
  - id: S1-02
    title: "User CRUD"
    body: "REST endpoints for user create/read/update/delete."
    acceptance: "All four endpoints return correct status codes."
    owner: dev
    sprint_id: SP1
    deps: [S1-01]
`

func runPublish(t *testing.T, st *tickets.Store, workdir string, inputs map[string]any) workflow.StepResult {
	t.Helper()
	r := &tickets.PublishRunner{Store: st}
	res, err := r.Run(context.Background(), workflow.Step{ID: "handoff", Type: "ticket_publish"}, inputs, workdir)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	return res
}

func writeBacklog(t *testing.T, workdir, relpath, content string) {
	t.Helper()
	full := filepath.Join(workdir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// TestPublishRunnerHappyPath: fixture → epic + 2 stories with correct deps.
func TestPublishRunnerHappyPath(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("expected success, got detail: %s", res.Detail)
	}
	if res.Output["epic"] != "E1" {
		t.Errorf("output epic: got %v, want E1", res.Output["epic"])
	}
	if res.Output["created"] != 2 {
		t.Errorf("output created: got %v, want 2", res.Output["created"])
	}
	if res.Output["skipped"] != 0 {
		t.Errorf("output skipped: got %v, want 0", res.Output["skipped"])
	}

	// Verify the epic was stored.
	epic, err := st.GetEpic("E1")
	if err != nil {
		t.Fatalf("GetEpic E1: %v", err)
	}
	if epic.Title != "Foundation" {
		t.Errorf("epic title: got %q, want Foundation", epic.Title)
	}

	// Verify stories and deps.
	s1, err := st.GetStory("S1-01")
	if err != nil {
		t.Fatalf("GetStory S1-01: %v", err)
	}
	if s1.EpicID != "E1" {
		t.Errorf("S1-01 epic_id: got %q, want E1", s1.EpicID)
	}
	if s1.Status != tickets.StatusBacklog {
		t.Errorf("S1-01 status: got %s, want backlog", s1.Status)
	}
	if len(s1.Deps) != 0 {
		t.Errorf("S1-01 deps: got %v, want empty", s1.Deps)
	}

	s2, err := st.GetStory("S1-02")
	if err != nil {
		t.Fatalf("GetStory S1-02: %v", err)
	}
	if len(s2.Deps) != 1 || s2.Deps[0] != "S1-01" {
		t.Errorf("S1-02 deps: got %v, want [S1-01]", s2.Deps)
	}
}

// depends_onFixture mirrors the canonical backlog but uses the `depends_on` alias
// a drifting planner prompt (the iteration-planner bug) emitted instead of `deps`.
const dependsOnFixture = `
epic:
  id: E1
  title: "Foundation"
stories:
  - id: S1-01
    title: "Auth service"
    body: "Implement the authentication service."
    acceptance: "Users can log in."
    owner: dev
    sprint_id: SP1
    depends_on: []
  - id: S1-02
    title: "User CRUD"
    body: "REST endpoints for users."
    acceptance: "All endpoints return correct codes."
    owner: dev
    sprint_id: SP1
    depends_on: [S1-01]
`

// Regression: a backlog using `depends_on` (instead of `deps`) must still produce
// stories WITH their dependencies — otherwise every story is dependency-free and the
// whole backlog fires in parallel (the serviciospty UI-redesign incident).
func TestPublishRunnerAcceptsDependsOnAlias(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", dependsOnFixture)

	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("expected success, got detail: %s", res.Detail)
	}
	s2, err := st.GetStory("S1-02")
	if err != nil {
		t.Fatalf("GetStory S1-02: %v", err)
	}
	if len(s2.Deps) != 1 || s2.Deps[0] != "S1-01" {
		t.Fatalf("S1-02 deps from depends_on: got %v, want [S1-01]", s2.Deps)
	}
}

// `deps` wins when both keys are present (canonical takes precedence).
func TestPublishRunnerDepsWinsOverDependsOn(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", `
epic: { id: E1, title: "F" }
stories:
  - id: S1-01
    title: "A"
    body: "a"
    acceptance: "ok"
    owner: dev
    sprint_id: SP1
    deps: []
  - id: S1-02
    title: "B"
    body: "b"
    acceptance: "ok"
    owner: dev
    sprint_id: SP1
    deps: [S1-01]
    depends_on: [S1-99]
`)
	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("expected success, got: %s", res.Detail)
	}
	s2, _ := st.GetStory("S1-02")
	if len(s2.Deps) != 1 || s2.Deps[0] != "S1-01" {
		t.Fatalf("deps should win over depends_on: got %v, want [S1-01]", s2.Deps)
	}
}

// TestPublishRunnerDerivesSprints: a backlog with sprint_id but NO `sprints:`
// section must still materialize the Sprint rows, otherwise sprint-batched mode
// (ReadySprints iterates the sprints table) would never fire.
func TestPublishRunnerDerivesSprints(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("expected success, got detail: %s", res.Detail)
	}
	if res.Output["sprints"] != 1 {
		t.Errorf("output sprints: got %v, want 1", res.Output["sprints"])
	}
	sprints, err := st.ListSprints()
	if err != nil {
		t.Fatalf("ListSprints: %v", err)
	}
	if len(sprints) != 1 || sprints[0].ID != "SP1" {
		t.Fatalf("derived sprints: got %v, want [SP1]", sprints)
	}
	// Name defaults to the id when derived.
	if sprints[0].Name != "SP1" {
		t.Errorf("derived sprint name: got %q, want SP1", sprints[0].Name)
	}
}

// TestPublishRunnerExplicitSprints: an explicit `sprints:` section carries the
// name/goal; derivation fills any sprint_id it omits.
func TestPublishRunnerExplicitSprints(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	const withSprints = `
epic:
  id: E1
  title: "Foundation"
  description: "Core"
sprints:
  - id: SP1
    name: "Sprint 1 — auth"
    goal: "Ship login"
stories:
  - id: S1-01
    title: "Auth"
    body: "b"
    acceptance: "a"
    owner: dev
    sprint_id: SP1
    deps: []
  - id: S1-02
    title: "Profile"
    body: "b"
    acceptance: "a"
    owner: dev
    sprint_id: SP2
    deps: [S1-01]
`
	writeBacklog(t, workdir, "docs/backlog.yaml", withSprints)
	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("expected success, got detail: %s", res.Detail)
	}
	if res.Output["sprints"] != 2 {
		t.Errorf("output sprints: got %v, want 2 (SP1 explicit + SP2 derived)", res.Output["sprints"])
	}
	sprints, err := st.ListSprints()
	if err != nil {
		t.Fatalf("ListSprints: %v", err)
	}
	byID := map[string]tickets.Sprint{}
	for _, sp := range sprints {
		byID[sp.ID] = sp
	}
	if byID["SP1"].Name != "Sprint 1 — auth" || byID["SP1"].Goal != "Ship login" {
		t.Errorf("SP1: got %+v, want explicit name/goal", byID["SP1"])
	}
	if byID["SP2"].ID != "SP2" {
		t.Errorf("SP2 not derived from story sprint_id: got %v", sprints)
	}
}

// TestPublishRunnerIdempotent: running twice with same fixture must not
// duplicate rows and must not error.
func TestPublishRunnerIdempotent(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	// First run.
	res1 := runPublish(t, st, workdir, nil)
	if !res1.Success {
		t.Fatalf("first run failed: %s", res1.Detail)
	}

	// Second run — must succeed (skipped=2, created=0).
	res2 := runPublish(t, st, workdir, nil)
	if !res2.Success {
		t.Fatalf("second run failed: %s", res2.Detail)
	}
	if res2.Output["created"] != 0 {
		t.Errorf("second run created: got %v, want 0", res2.Output["created"])
	}
	if res2.Output["skipped"] != 2 {
		t.Errorf("second run skipped: got %v, want 2", res2.Output["skipped"])
	}

	// Confirm no duplicates: still 2 stories.
	stories, err := st.ListStories()
	if err != nil {
		t.Fatalf("ListStories: %v", err)
	}
	if len(stories) != 2 {
		t.Errorf("story count after 2 runs: got %d, want 2", len(stories))
	}
}

// TestPublishRunnerSurfacesSkippedIDs: re-publishing a backlog whose ids already
// exist (e.g. an iteration whose planner reused an id) must report the CONCRETE
// colliding ids in the step output, not just a count — otherwise the story is
// dropped silently and the user can't tell which one.
func TestPublishRunnerSurfacesSkippedIDs(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	if res := runPublish(t, st, workdir, nil); !res.Success {
		t.Fatalf("first run failed: %s", res.Detail)
	}

	// Second publish: both ids collide → skipped, and their ids are surfaced.
	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("second run failed: %s", res.Detail)
	}
	ids, ok := res.Output["skipped_ids"].([]string)
	if !ok {
		t.Fatalf("skipped_ids missing or wrong type: %T %v", res.Output["skipped_ids"], res.Output["skipped_ids"])
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["S1-01"] || !got["S1-02"] {
		t.Errorf("skipped_ids: got %v, want both S1-01 and S1-02", ids)
	}
	if !strings.Contains(res.Detail, "S1-01") || !strings.Contains(res.Detail, "S1-02") {
		t.Errorf("detail should name the skipped ids: %q", res.Detail)
	}
}

// TestPublishRunnerMissingFile: absent backlog → failed StepResult, no panic.
func TestPublishRunnerMissingFile(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir() // no backlog.yaml written

	res := runPublish(t, st, workdir, nil)
	if res.Success {
		t.Fatal("expected failure for missing file, got success")
	}
	if res.Detail == "" {
		t.Error("expected non-empty Detail for missing file error")
	}
}

// TestPublishRunnerGarbageYAML: unparseable content → failed StepResult, no panic.
func TestPublishRunnerGarbageYAML(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	// Use YAML that is genuinely malformed (unclosed flow sequence).
	writeBacklog(t, workdir, "docs/backlog.yaml", "{unclosed: [bracket")

	res := runPublish(t, st, workdir, nil)
	if res.Success {
		t.Fatal("expected failure for garbage YAML, got success")
	}
	if res.Detail == "" {
		t.Error("expected non-empty Detail for parse error")
	}
}

// TestPublishRunnerCustomPath: inputs["backlog"] overrides the default path.
func TestPublishRunnerCustomPath(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "custom/path.yaml", fixture)

	res := runPublish(t, st, workdir, map[string]any{"backlog": "custom/path.yaml"})
	if !res.Success {
		t.Fatalf("custom path failed: %s", res.Detail)
	}
	if res.Output["created"] != 2 {
		t.Errorf("created: got %v, want 2", res.Output["created"])
	}
}

// TestPublishRunnerSetsRepo: inputs["repo"] is stored on every created story.
func TestPublishRunnerSetsRepo(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	const wantRepo = "https://github.com/acme/myproject"
	res := runPublish(t, st, workdir, map[string]any{"repo": wantRepo})
	if !res.Success {
		t.Fatalf("publish failed: %s", res.Detail)
	}

	// Both stories must carry the repo URL.
	for _, id := range []string{"S1-01", "S1-02"} {
		st2, err := st.GetStory(id)
		if err != nil {
			t.Fatalf("GetStory %s: %v", id, err)
		}
		if st2.Repo != wantRepo {
			t.Errorf("story %s repo: got %q, want %q", id, st2.Repo, wantRepo)
		}
	}
}

// screenKeyFixture carries a frontend story with a screen_key and a backend story
// without one — the scrum-master emits screen_key only for stories that own a screen.
const screenKeyFixture = `
epic:
  id: E1
  title: "Foundation"
stories:
  - id: S1-01
    title: "Login screen"
    body: "Build the login UI."
    acceptance: "User can log in."
    owner: react-dev
    sprint_id: SP1
    screen_key: customer.login
    deps: []
  - id: S1-02
    title: "Auth endpoint"
    body: "Issue JWTs."
    acceptance: "Token minted."
    owner: python-dev
    sprint_id: SP1
    deps: [S1-01]
`

// TestPublishRunnerPersistsScreenKey: publishing writes each story's screen_key from
// the backlog into the store column (the store, not backlog.yaml, is now the truth).
func TestPublishRunnerPersistsScreenKey(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", screenKeyFixture)

	if res := runPublish(t, st, workdir, nil); !res.Success {
		t.Fatalf("publish failed: %s", res.Detail)
	}

	front, err := st.GetStory("S1-01")
	if err != nil {
		t.Fatalf("GetStory S1-01: %v", err)
	}
	if front.ScreenKey != "customer.login" {
		t.Errorf("S1-01 screen_key: got %q, want customer.login", front.ScreenKey)
	}
	// A backend story without a screen_key gets an empty column, not a spurious value.
	back, err := st.GetStory("S1-02")
	if err != nil {
		t.Fatalf("GetStory S1-02: %v", err)
	}
	if back.ScreenKey != "" {
		t.Errorf("S1-02 screen_key: got %q, want empty", back.ScreenKey)
	}
}

// TestPublishRunnerScreenKeyIdempotent: a re-publish (iterate) whose backlog reuses an
// existing id must NOT overwrite the story already in the store — including its
// screen_key. Confirms the append-idempotent contract for the new column.
func TestPublishRunnerScreenKeyIdempotent(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", screenKeyFixture)

	if res := runPublish(t, st, workdir, nil); !res.Success {
		t.Fatalf("first publish failed: %s", res.Detail)
	}

	// A second backlog reusing S1-01's id but flipping its screen_key. The id collides,
	// so the store row is preserved — the original screen_key must NOT be clobbered.
	writeBacklog(t, workdir, "docs/backlog.yaml", `
epic: { id: E1, title: "F" }
stories:
  - id: S1-01
    title: "Login screen v2"
    owner: react-dev
    sprint_id: SP1
    screen_key: customer.WRONG
    deps: []
`)
	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("second publish failed: %s", res.Detail)
	}
	if res.Output["skipped"] != 1 {
		t.Fatalf("second publish skipped: got %v, want 1", res.Output["skipped"])
	}
	got, _ := st.GetStory("S1-01")
	if got.ScreenKey != "customer.login" {
		t.Errorf("screen_key was overwritten on re-publish: got %q, want customer.login", got.ScreenKey)
	}
}

// TestPublishRunnerNoRepo: when inputs["repo"] is absent, stories get an empty repo.
func TestPublishRunnerNoRepo(t *testing.T) {
	st := openTemp(t)
	workdir := t.TempDir()
	writeBacklog(t, workdir, "docs/backlog.yaml", fixture)

	res := runPublish(t, st, workdir, nil)
	if !res.Success {
		t.Fatalf("publish failed: %s", res.Detail)
	}

	got, err := st.GetStory("S1-01")
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	if got.Repo != "" {
		t.Errorf("expected empty repo, got %q", got.Repo)
	}
}
