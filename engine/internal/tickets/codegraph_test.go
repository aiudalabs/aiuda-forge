package tickets_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"forge/internal/tickets"
)

func openStore(t *testing.T) *tickets.Store {
	t.Helper()
	st, err := tickets.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return st
}

func TestRecordStoryFilesIdempotent(t *testing.T) {
	st := openStore(t)
	paths := []string{"frontend/src/App.tsx", "frontend/src/App.tsx", "  ", "backend/api/users.py"}

	n, err := st.RecordStoryFiles("p1", "S1", paths)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if n != 2 { // dup + blank collapse to 2 distinct
		t.Fatalf("first insert = %d, want 2", n)
	}
	// Re-recording the same merge is a no-op (the projection loop re-runs).
	n, err = st.RecordStoryFiles("p1", "S1", paths)
	if err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if n != 0 {
		t.Fatalf("re-insert = %d, want 0 (idempotent)", n)
	}

	got, err := st.FilesForStory("p1", "S1")
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	want := []string{"backend/api/users.py", "frontend/src/App.tsx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestRecordStoryFilesNoop(t *testing.T) {
	st := openStore(t)
	if n, err := st.RecordStoryFiles("p1", "S1", nil); err != nil || n != 0 {
		t.Fatalf("nil paths: n=%d err=%v", n, err)
	}
	if n, err := st.RecordStoryFiles("p1", "", []string{"a.go"}); err != nil || n != 0 {
		t.Fatalf("empty story: n=%d err=%v", n, err)
	}
}

func TestModuleMapAggregatesAndScopes(t *testing.T) {
	st := openStore(t)
	// Two projects to prove project scoping.
	if err := st.CreateStory(tickets.Story{ID: "S1", ProjectID: "p1", Owner: "react-dev"}); err != nil {
		t.Fatalf("story S1: %v", err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S2", ProjectID: "p1", Owner: "python-dev"}); err != nil {
		t.Fatalf("story S2: %v", err)
	}
	if err := st.CreateStory(tickets.Story{ID: "S9", ProjectID: "p2", Owner: "react-dev"}); err != nil {
		t.Fatalf("story S9: %v", err)
	}
	mustRecord(t, st, "p1", "S1", "frontend/src/App.tsx", "frontend/src/Home.tsx", "frontend/lib/api.ts")
	mustRecord(t, st, "p1", "S2", "frontend/src/Login.tsx", "backend/api/users.py")
	mustRecord(t, st, "p2", "S9", "frontend/src/Other.tsx") // different project — must not leak

	mods, err := st.ModuleMap("p1", 2)
	if err != nil {
		t.Fatalf("modulemap: %v", err)
	}
	// depth 2: frontend/src (3 files: App, Home, Login), frontend/lib (1), backend/api (1).
	if len(mods) != 3 {
		t.Fatalf("got %d modules, want 3: %+v", len(mods), mods)
	}
	top := mods[0]
	if top.Dir != "frontend/src" || top.Files != 3 {
		t.Fatalf("top module = %+v, want frontend/src with 3 files", top)
	}
	if !reflect.DeepEqual(top.Stories, []string{"S1", "S2"}) {
		t.Fatalf("top stories = %v, want [S1 S2]", top.Stories)
	}
	if !reflect.DeepEqual(top.Lanes, []string{"python-dev", "react-dev"}) {
		t.Fatalf("top lanes = %v, want [python-dev react-dev]", top.Lanes)
	}
	// Ordering: files desc, then dir asc. backend/api and frontend/lib both have
	// 1 file → alphabetical.
	if mods[1].Dir != "backend/api" || mods[2].Dir != "frontend/lib" {
		t.Fatalf("tie order = %s,%s, want backend/api,frontend/lib", mods[1].Dir, mods[2].Dir)
	}
}

func mustRecord(t *testing.T, st *tickets.Store, project, story string, paths ...string) {
	t.Helper()
	if _, err := st.RecordStoryFiles(project, story, paths); err != nil {
		t.Fatalf("record %s/%s: %v", project, story, err)
	}
}
