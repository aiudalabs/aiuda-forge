package tickets_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"forge/internal/tickets"
)

// Regression (companion to the #17 kind fix): a tickets DB created BEFORE the
// `screen_key` column must open cleanly. screen_key is added by migrationAddScreenKey
// and then the row is rebuilt by migrateToCompositePK — the exact sequence that broke
// with `kind`: the ALTER must run BEFORE the `SELECT *` rebuild so the live table and
// stories_new have matching column sets. A legacy single-PK row must survive the
// rebuild, default to screen_key='', and new rows must round-trip a screen_key.
func TestOpenMigratesLegacyDBWithoutScreenKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Legacy stories table WITH kind but WITHOUT screen_key, single-column PRIMARY KEY
	// (so migrateToCompositePK runs the SELECT * rebuild), plus a pre-existing row.
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
		project_id TEXT NOT NULL DEFAULT '', external_ref TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT 'story'
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

	// The legacy row survived the rebuild and defaulted to an empty screen_key.
	got, err := st.GetStoryInProject("p1", "old-1")
	if err != nil {
		t.Fatalf("legacy row lost: %v", err)
	}
	if got.ScreenKey != "" {
		t.Fatalf("legacy screen_key = %q, want empty", got.ScreenKey)
	}

	// The migrated column round-trips a screen_key on a new story.
	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "Login UI", ProjectID: "p1", ScreenKey: "customer.login"}); err != nil {
		t.Fatalf("CreateStory with screen_key: %v", err)
	}
	if got, _ := st.GetStoryInProject("p1", "S1"); got.ScreenKey != "customer.login" {
		t.Fatalf("screen_key = %q, want customer.login", got.ScreenKey)
	}
}
