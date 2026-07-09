package projects_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"forge/internal/projects"
)

func openTemp(t *testing.T) *projects.Store {
	t.Helper()
	st, err := projects.Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCreateAndGet(t *testing.T) {
	st := openTemp(t)

	p := projects.Project{
		ID:          "proj-001",
		Name:        "my project",
		Description: "test",
		Repo:        "https://github.com/org/my-project",
	}
	created, err := st.Create(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedAt == 0 {
		t.Error("created_at should be set")
	}

	got, err := st.Get("proj-001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "my project" {
		t.Errorf("name: got %q, want %q", got.Name, "my project")
	}
	if got.Repo != "https://github.com/org/my-project" {
		t.Errorf("repo: got %q", got.Repo)
	}
}

func TestGetNotFound(t *testing.T) {
	st := openTemp(t)
	_, err := st.Get("missing")
	if err != projects.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateDuplicate(t *testing.T) {
	st := openTemp(t)

	p := projects.Project{ID: "dup", Name: "dup", Repo: "https://github.com/org/dup"}
	if _, err := st.Create(p); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := st.Create(p)
	if err == nil {
		t.Fatal("expected error on duplicate insert")
	}
	// The error must wrap or mention ErrExists.
	if err != projects.ErrExists && !containsStr(err.Error(), "already exists") {
		t.Errorf("expected ErrExists, got %v", err)
	}
}

func TestList(t *testing.T) {
	st := openTemp(t)

	// Insert two projects with explicit timestamps so ordering is deterministic.
	base := time.Now().UnixMilli()
	p1 := projects.Project{ID: "P1", Name: "First", Repo: "https://github.com/org/first", CreatedAt: base}
	p2 := projects.Project{ID: "P2", Name: "Second", Repo: "https://github.com/org/second", CreatedAt: base + 1}

	for _, p := range []projects.Project{p1, p2} {
		if _, err := st.Create(p); err != nil {
			t.Fatalf("create %s: %v", p.ID, err)
		}
	}

	list, err := st.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Open auto-creates the "default" backfill project (audit A1), so List returns
	// it alongside the two created here. Drop it and assert on the rest.
	var got []projects.Project
	for _, p := range list {
		if p.ID == projects.DefaultProjectID {
			continue
		}
		got = append(got, p)
	}
	if len(got) != 2 {
		t.Fatalf("list len (excluding default): got %d, want 2", len(got))
	}
	// Among the created projects, ordered by created_at DESC → P2 before P1.
	if got[0].ID != "P2" || got[1].ID != "P1" {
		t.Errorf("order: got %q,%q, want P2,P1", got[0].ID, got[1].ID)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestListByOwner: GET /projects scoping — ListByOwner returns only one user's
// projects (audit A1).
func TestListByOwner(t *testing.T) {
	st := openTemp(t)
	mustCreate(t, st, projects.Project{ID: "u1a", Name: "A", OwnerID: "usr-1"})
	mustCreate(t, st, projects.Project{ID: "u1b", Name: "B", OwnerID: "usr-1"})
	mustCreate(t, st, projects.Project{ID: "u2a", Name: "C", OwnerID: "usr-2"})

	one, err := st.ListByOwner("usr-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 2 {
		t.Fatalf("usr-1 should own 2 projects, got %d", len(one))
	}
	for _, p := range one {
		if p.OwnerID != "usr-1" {
			t.Errorf("leaked project owned by %q into usr-1's list", p.OwnerID)
		}
	}
}

// TestSettingsRoundTripAndValidation: per-project execution settings default to
// sprint/manual, accept story/auto, and reject unknown values (audit A2).
func TestSettingsRoundTripAndValidation(t *testing.T) {
	st := openTemp(t)
	mustCreate(t, st, projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"})

	got, err := st.GetSettings("P")
	if err != nil {
		t.Fatal(err)
	}
	if got.ExecutionUnit != projects.ExecutionUnitSprint || got.MergeMode != projects.MergeModeManual {
		t.Fatalf("defaults: got %+v, want sprint/manual", got)
	}

	out, err := st.PutSettings("P", projects.Settings{ExecutionUnit: "story", MergeMode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ExecutionUnit != "story" || out.MergeMode != "auto" {
		t.Fatalf("round-trip: got %+v, want story/auto", out)
	}

	if _, err := st.PutSettings("P", projects.Settings{ExecutionUnit: "epic"}); !errors.Is(err, projects.ErrInvalid) {
		t.Fatalf("invalid execution_unit should be ErrInvalid, got %v", err)
	}
	if _, err := st.PutSettings("P", projects.Settings{MergeMode: "rebase"}); !errors.Is(err, projects.ErrInvalid) {
		t.Fatalf("invalid merge_mode should be ErrInvalid, got %v", err)
	}
	// A rejected PUT must not corrupt the stored value.
	after, _ := st.GetSettings("P")
	if after.ExecutionUnit != "story" || after.MergeMode != "auto" {
		t.Fatalf("rejected PUT corrupted settings: %+v", after)
	}

	if _, err := st.GetSettings("nope"); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("settings for unknown project should be ErrNotFound, got %v", err)
	}
}

// TestDefaultProjectExists: Open always materializes the "default" backfill
// project (owner_id="") so pre-multi-tenant data has a real row to point at.
func TestDefaultProjectExists(t *testing.T) {
	st := openTemp(t)
	d, err := st.Get(projects.DefaultProjectID)
	if err != nil {
		t.Fatalf("default project should exist: %v", err)
	}
	if d.OwnerID != "" {
		t.Errorf("default project owner_id should be empty, got %q", d.OwnerID)
	}
	if d.ExecutionUnit != projects.ExecutionUnitSprint || d.MergeMode != projects.MergeModeManual {
		t.Errorf("default project settings: got %s/%s, want sprint/manual", d.ExecutionUnit, d.MergeMode)
	}
}

func mustCreate(t *testing.T, st *projects.Store, p projects.Project) {
	t.Helper()
	if _, err := st.Create(p); err != nil {
		t.Fatalf("create %s: %v", p.ID, err)
	}
}

func TestReleaseConfig(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "p1", Repo: "https://github.com/o/r"}); err != nil {
		t.Fatal(err)
	}

	// A fresh project defaults to the static release target, no firebase token.
	got, err := st.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReleaseTarget != projects.ReleaseTargetStatic {
		t.Fatalf("default release_target = %q, want %q", got.ReleaseTarget, projects.ReleaseTargetStatic)
	}
	if got.FirebaseToken != "" {
		t.Fatalf("default firebase_token should be empty, got %q", got.FirebaseToken)
	}

	// Set firebase + token.
	if err := st.SetReleaseConfig("p1", projects.ReleaseTargetFirebase, "tok-123"); err != nil {
		t.Fatalf("SetReleaseConfig: %v", err)
	}
	got, _ = st.Get("p1")
	if got.ReleaseTarget != projects.ReleaseTargetFirebase || got.FirebaseToken != "tok-123" {
		t.Fatalf("after set: target=%q token=%q", got.ReleaseTarget, got.FirebaseToken)
	}

	// A target-only update (empty token) preserves the stored token.
	if err := st.SetReleaseConfig("p1", projects.ReleaseTargetStatic, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get("p1")
	if got.ReleaseTarget != projects.ReleaseTargetStatic || got.FirebaseToken != "tok-123" {
		t.Fatalf("target-only update: target=%q token=%q (token should persist)", got.ReleaseTarget, got.FirebaseToken)
	}

	// Invalid target → ErrInvalid; unknown project → ErrNotFound.
	if err := st.SetReleaseConfig("p1", "s3", ""); !errors.Is(err, projects.ErrInvalid) {
		t.Fatalf("invalid target err = %v, want ErrInvalid", err)
	}
	if err := st.SetReleaseConfig("nope", projects.ReleaseTargetStatic, ""); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("unknown project err = %v, want ErrNotFound", err)
	}
}
