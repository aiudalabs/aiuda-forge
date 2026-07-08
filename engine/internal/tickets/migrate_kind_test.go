package tickets_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"forge/internal/tickets"
)

// Regression: a tickets DB created BEFORE the `kind` column must open cleanly, migrate
// the column in, default pre-existing rows to 'story', and accept new bug/story rows.
func TestOpenMigratesLegacyDBWithoutKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Build a legacy stories table WITH external_ref but WITHOUT kind, plus a row.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", path)
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE stories (
		id TEXT PRIMARY KEY,
		epic_id TEXT NOT NULL DEFAULT '', sprint_id TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '',
		accept TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'backlog', run_id TEXT NOT NULL DEFAULT '',
		repo TEXT NOT NULL DEFAULT '', pr_url TEXT NOT NULL DEFAULT '',
		project_id TEXT NOT NULL DEFAULT '', external_ref TEXT NOT NULL DEFAULT ''
	); INSERT INTO stories(id,status,project_id) VALUES('old-1','backlog','p1');`)
	if err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}
	raw.Close()

	st, err := tickets.Open(path)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer st.Close()

	// The legacy row survived and defaulted to kind 'story'.
	got, err := st.GetStoryInProject("p1", "old-1")
	if err != nil {
		t.Fatalf("legacy row lost: %v", err)
	}
	if got.Kind != tickets.KindStory {
		t.Fatalf("legacy kind = %q, want story", got.Kind)
	}
	// The migrated column is usable for a new bug.
	if err := st.CreateStory(tickets.Story{ID: "bug-1", Title: "defect", ProjectID: "p1", Kind: tickets.KindBug}); err != nil {
		t.Fatalf("CreateStory with kind: %v", err)
	}
	if got, _ := st.GetStoryInProject("p1", "bug-1"); got.Kind != tickets.KindBug {
		t.Fatalf("kind = %q, want bug", got.Kind)
	}
}
