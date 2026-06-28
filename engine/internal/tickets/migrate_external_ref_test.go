package tickets_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"forge/internal/tickets"
)

// Regression: a tickets DB created BEFORE the external_ref column must open cleanly.
// The bug (caught live, not by fresh-DB tests) was creating the external_ref UNIQUE
// index inside the schema string, which runs before the ADD COLUMN migration — so on
// a pre-existing stories table the index referenced a column that didn't exist yet.
// Here we hand-build an old-style stories table (no external_ref) and assert Open
// migrates it and the column is usable.
func TestOpenMigratesLegacyDBWithoutExternalRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Build a legacy stories table WITHOUT external_ref, plus a pre-existing row.
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
		project_id TEXT NOT NULL DEFAULT ''
	); INSERT INTO stories(id,status,project_id) VALUES('old-1','backlog','p1');`)
	if err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}
	raw.Close()

	// Open must succeed (migrate the column + index) — this used to fail.
	st, err := tickets.Open(path)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer st.Close()

	// The new external_ref column is usable: create an imported story + look it up.
	if err := st.CreateStory(tickets.Story{ID: "imp-1", Title: "imported", ProjectID: "p1", ExternalRef: "github:a/b#1"}); err != nil {
		t.Fatalf("CreateStory with external_ref: %v", err)
	}
	id, ok, err := st.StoryIDByExternalRef("github:a/b#1")
	if err != nil || !ok || id != "imp-1" {
		t.Fatalf("StoryIDByExternalRef = %q,%v,%v, want imp-1", id, ok, err)
	}
	// The legacy row survived.
	if _, err := st.GetStory("old-1"); err != nil {
		t.Fatalf("legacy row lost: %v", err)
	}
}
