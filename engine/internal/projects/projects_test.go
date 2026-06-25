package projects_test

import (
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
	if len(list) != 2 {
		t.Fatalf("list len: got %d, want 2", len(list))
	}
	// Ordered by created_at DESC → P2 first.
	if list[0].ID != "P2" {
		t.Errorf("first item: got %q, want P2", list[0].ID)
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
