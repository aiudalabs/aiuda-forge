// Package tickets is the native ticket store (control-plane, not kernel).
// It is the source of truth for the backlog: epics, sprints, stories, deps,
// and derived readiness. GitHub/JIRA become optional sync targets later.
// The store opens its OWN sqlite file and never touches the kernel's runs DB.
package tickets

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Status is the lifecycle column (Kanban vocabulary). "ready" is DERIVED —
// see Store.Ready(); it is never written to the DB.
type Status string

const (
	StatusBacklog  Status = "backlog"
	StatusReady    Status = "ready" // derived only — not stored
	StatusRunning  Status = "running"
	StatusInReview Status = "in_review"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
	// StatusCancelled is a terminal state a human moves a story to when the work is
	// abandoned mid-flight (scope cut, superseded, or split into smaller stories). It
	// is NEVER reached automatically — only via the explicit cancel operation — and,
	// like done, it is terminal (no legal transition out of it). A story that is
	// actively `running` cannot be cancelled directly: cancel its kernel run first
	// (which parks the story failed), then cancel from there.
	StatusCancelled Status = "cancelled"
)

// Kind classifies a story by the type of work. "story" is a feature/change; "bug"
// is a defect. It is metadata for the Board (filtering, iconography) and does not
// affect the lifecycle. Defaults to KindStory.
const (
	KindStory = "story"
	KindBug   = "bug"
)

// validKind reports whether k is a recognised story kind. Empty is treated as
// valid by callers that default it to KindStory.
func validKind(k string) bool { return k == KindStory || k == KindBug }

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrIllegalTransition is returned when a mutator is asked to move a story from a
// state that is not a legal source for the requested target (e.g. resurrecting a
// terminal done/failed story). It mirrors the kernel store's legalTransitions
// discipline (internal/store/types.go): the set of stored states a transition may
// legally start from is the single source of truth, and any other transition is a
// no-op that surfaces this typed error rather than silently corrupting state.
var ErrIllegalTransition = errors.New("illegal status transition")

// ErrDepCycle is returned when adding a dependency would create a cycle (or a
// self-dependency), which would deadlock readiness forever.
var ErrDepCycle = errors.New("dependency cycle")

// ErrDepNotFound is returned when a story declares a dependency on an id that does
// not resolve to an existing story — that dep can never reach "done", so the
// dependent would deadlock silently. Caught at CreateStory/AddDep instead.
var ErrDepNotFound = errors.New("dependency story not found")

// ErrInvalidState is returned by the human "team-move" operations (move/split/edit)
// when the target story is in a state that operation forbids — e.g. moving or
// splitting a story that is not in backlog/failed, or editing a running/terminal
// story. It is distinct from ErrIllegalTransition (which guards the automatic
// lifecycle): this guards the operator-driven mutations. Handlers map it to 409.
var ErrInvalidState = errors.New("operation not allowed in current state")

// ErrDepOrder is returned when moving a story to a sprint would break the sprint
// ordering its dependency graph implies: a story may not sit in a sprint EARLIER
// than any story it depends on (it would be fired before its prerequisite), nor
// LATER than any story that depends on it (that dependent would be fired first).
// Handlers map it to 409.
var ErrDepOrder = errors.New("move breaks dependency ordering")

// ErrInvalidInput is returned for malformed operation arguments the caller can fix —
// e.g. a split with fewer than two parts, or a part id that collides with an existing
// story. Distinct from ErrInvalidState (a state guard) so handlers map it to 400 while
// a genuine DB failure still surfaces as 500.
var ErrInvalidInput = errors.New("invalid input")

// legalSources maps each writable target status to the set of stored statuses a
// transition into it may legally start from. "ready" is derived (never stored) and
// has no row. This is the tickets-store analogue of the kernel store's
// legalTransitions table. A mutator whose UPDATE carries the matching
// `AND status IN (...)` predicate makes an illegal transition a 0-row no-op, which
// the mutators translate into ErrIllegalTransition (vs ErrNotFound for a missing id).
var legalSources = map[Status][]Status{
	// A claim takes backlog → running (handled by ClaimStory directly).
	StatusRunning: {StatusBacklog},
	// A run finishing parks running → in_review (PR opened, not yet merged).
	StatusInReview: {StatusRunning},
	// A merge advances in_review → done. The legacy non-merge-gated direct path
	// (running → done) is intentionally NOT permitted here: only a merged PR (which
	// first parks the story in_review) may reach done, so a still-running story can
	// never be flipped done by another story's merge.
	StatusDone: {StatusInReview},
	// A failure may strike any non-terminal state (backlog/running/in_review).
	StatusFailed: {StatusBacklog, StatusRunning, StatusInReview},
	// A human cancel abandons a story from any non-running, non-terminal state:
	// backlog (which is also what a derived "ready" story is stored as), failed, or
	// in_review. `running` is deliberately EXCLUDED — a live run must be cancelled
	// first (it then parks the story failed, from where cancel is legal). done is
	// terminal and never re-opened. This makes cancel-of-running a 0-row no-op that
	// surfaces ErrIllegalTransition rather than orphaning a live run's PR.
	StatusCancelled: {StatusBacklog, StatusFailed, StatusInReview},
}

// inClause renders a SQL `status IN ('a','b',...)` fragment from a status set. The
// values are a fixed internal vocabulary (never user input), so inlining them is
// safe and keeps the guarded UPDATEs readable.
func inClause(states []Status) string {
	quoted := make([]string, len(states))
	for i, s := range states {
		quoted[i] = "'" + string(s) + "'"
	}
	return "status IN (" + strings.Join(quoted, ",") + ")"
}

// Epic is a high-level grouping of Stories.
type Epic struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Sprint is a time-box that Stories can be assigned to. ProjectID scopes the
// sprint to its project (audit A1); empty defaults to DefaultProjectID on create.
type Sprint struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Goal      string `json:"goal"`
	ProjectID string `json:"project_id"`
	// PlannedAt is the unix-millis timestamp the sprint-planning ceremony approved
	// and applied this sprint's plan (0 = never planned). In "ceremony" planning
	// mode the native scheduler refuses to fire a sprint until PlannedAt != 0; in
	// "auto" mode it is ignored. Set by the plan_apply step.
	PlannedAt int64 `json:"planned_at"`
	// PlanningRunID is the control-plane run of the sprint-planning workflow the
	// scheduler last started for this sprint. It is the PERSISTED idempotency
	// reference (not process memory): the scheduler will not start a second planning
	// run while this one is still live. Empty = no planning run started yet.
	PlanningRunID string `json:"planning_run_id"`
	// ReviewedAt is the unix-millis timestamp the sprint-review ceremony accepted this
	// DONE sprint's increment (0 = never reviewed). In "ceremony" review mode the
	// native scheduler holds the project's next sprint until ReviewedAt != 0; in
	// "auto" mode it is ignored. Set by the review_close step.
	ReviewedAt int64 `json:"reviewed_at"`
	// ReviewRunID is the control-plane run of the sprint-review workflow the scheduler
	// last started for this sprint — the PERSISTED idempotency reference (twin of
	// PlanningRunID): the scheduler will not start a second review run while this one
	// is still live. Empty = no review run started yet.
	ReviewRunID string `json:"review_run_id"`
	// RetroAt is the unix-millis timestamp the sprint-retrospective ceremony applied its
	// method proposals (0 = never retro'd). In "ceremony" retro mode the native
	// scheduler holds the project's next sprint until RetroAt != 0 (after ReviewedAt);
	// "auto" mode ignores it. Set by the registry_apply step.
	RetroAt int64 `json:"retro_at"`
	// RetroRunID is the control-plane run of the sprint-retro workflow the scheduler
	// last started for this sprint — the PERSISTED idempotency reference (twin of
	// ReviewRunID). Empty = no retro run started yet.
	RetroRunID string `json:"retro_run_id"`
}

// Story is the unit of work. epic_id and sprint_id are optional. deps is the
// list of story IDs that must reach StatusDone before this story is "ready".
// run_id records the control-plane run that is executing this story.
// repo is the GitHub repository URL the factory run clones to implement this story.
type Story struct {
	ID       string   `json:"id"`
	EpicID   string   `json:"epic_id,omitempty"`
	SprintID string   `json:"sprint_id,omitempty"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Accept   string   `json:"acceptance"`
	Owner    string   `json:"owner,omitempty"`
	Deps     []string `json:"deps"`
	Status   Status   `json:"status"`
	RunID    string   `json:"run_id,omitempty"`
	Repo     string   `json:"repo,omitempty"`
	// PRURL is the pull request the story's run opened. It is recorded when the
	// run finishes (status → in_review) so the merge-reconcile loop can check
	// whether that PR has been merged before advancing the story to done.
	PRURL string `json:"pr_url,omitempty"`
	// ProjectID scopes the story to its project (audit A1). Stories created via
	// POST carry it; stories published via ticket_publish inherit the design run's
	// project_id. Empty defaults to DefaultProjectID on create.
	ProjectID string `json:"project_id,omitempty"`
	// ExternalRef ties a story to an external source issue for idempotent import
	// (v1.3), e.g. "github:owner/repo#42". Empty for natively-created stories.
	// UNIQUE when non-empty, so re-importing the same issue is a no-op.
	ExternalRef string `json:"external_ref,omitempty"`
	// Kind classifies the work: KindStory (default) or KindBug. Board-only metadata;
	// it does not affect the lifecycle.
	Kind string `json:"kind,omitempty"`
	// ScreenKey names the mockup/screen a frontend story implements
	// (docs/mockups/<screen_key>.html). Emitted by the scrum-master in the design
	// backlog; persisted here so the store — not the immutable docs/backlog.yaml
	// snapshot — is the source of truth after mid-sprint moves. Empty for backend
	// stories and for stories published before the screen_key migration.
	ScreenKey string `json:"screen_key,omitempty"`
}

// StoryDraft is the shape of ONE new story produced by splitting an existing one.
// The split inherits the original's epic, sprint, kind, and dependency edges, so a
// draft only carries the per-part fields the operator rewrites. An empty ID asks the
// store to derive a deterministic suffix (<original>-a, <original>-b, …).
type StoryDraft struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Accept string `json:"acceptance"`
	// Owner overrides the inherited owner (lane) for this part; empty keeps the
	// original's owner.
	Owner string `json:"owner,omitempty"`
}

// StoryPatch is a sparse edit to a story: only non-nil fields are applied. Deps,
// when non-nil, REPLACES the story's dependency set (an empty non-nil slice clears
// them) and revalidates the graph (no self-dep, every dep exists, no cycle). Kind
// is intentionally absent — it is set at creation, not edited here.
type StoryPatch struct {
	Title     *string   `json:"title,omitempty"`
	Body      *string   `json:"body,omitempty"`
	Accept    *string   `json:"acceptance,omitempty"`
	Owner     *string   `json:"owner,omitempty"`
	ScreenKey *string   `json:"screen_key,omitempty"`
	Deps      *[]string `json:"deps,omitempty"`
}

// DefaultProjectID is the project that pre-multi-tenant stories/sprints are
// backfilled to so single-tenant backlogs survive the migration. It mirrors
// store.DefaultProjectID (the same "default" project the kernel uses).
const DefaultProjectID = "default"

const schema = `
CREATE TABLE IF NOT EXISTS epics (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sprints (
  id              TEXT NOT NULL,
  name            TEXT NOT NULL DEFAULT '',
  goal            TEXT NOT NULL DEFAULT '',
  project_id      TEXT NOT NULL DEFAULT 'default',
  planned_at      INTEGER NOT NULL DEFAULT 0,
  planning_run_id TEXT NOT NULL DEFAULT '',
  reviewed_at     INTEGER NOT NULL DEFAULT 0,
  review_run_id   TEXT NOT NULL DEFAULT '',
  retro_at        INTEGER NOT NULL DEFAULT 0,
  retro_run_id    TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (id, project_id)
);

CREATE TABLE IF NOT EXISTS stories (
  id         TEXT PRIMARY KEY,
  epic_id    TEXT NOT NULL DEFAULT '',
  sprint_id  TEXT NOT NULL DEFAULT '',
  title      TEXT NOT NULL DEFAULT '',
  body       TEXT NOT NULL DEFAULT '',
  accept     TEXT NOT NULL DEFAULT '',
  owner      TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'backlog',
  run_id     TEXT NOT NULL DEFAULT '',
  repo       TEXT NOT NULL DEFAULT '',
  pr_url     TEXT NOT NULL DEFAULT '',
  project_id TEXT NOT NULL DEFAULT '',
  external_ref TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL DEFAULT 'story',
  screen_key TEXT NOT NULL DEFAULT ''
);
-- NOTE: the external_ref UNIQUE index is created in Open() AFTER the
-- migrationAddExternalRef ALTER, not here: an existing DB's stories table predates
-- the column, so indexing it inside this schema string (which runs before the
-- migration) would fail with "no such column: external_ref".

CREATE TABLE IF NOT EXISTS story_deps (
  story_id TEXT NOT NULL,
  dep_id   TEXT NOT NULL,
  PRIMARY KEY (story_id, dep_id)
);

-- La sesión de agente de GitHub que está ejecutando una story despachada (F2):
-- la URL del Copilot task / workflow run. Tabla lateral (no columna) para no
-- tocar los cinco SELECT de stories; se lee en bloque para el ticketView.
CREATE TABLE IF NOT EXISTS story_sessions (
  story_id TEXT PRIMARY KEY,
  url      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_story_deps_story ON story_deps(story_id);
CREATE INDEX IF NOT EXISTS idx_story_deps_dep   ON story_deps(dep_id);

-- El "cerebro" producto↔código (task #5): las rutas que el PR mergeado de una
-- story tocó de verdad. Es la única arista del grafo que no se deriva ya de
-- stories/deps (esos son nativos en Issues). Se puebla en el pase de proyección
-- cuando una story llega a done por un PR mergeado (github.ListPRFiles). Tabla
-- nueva → sin ALTER de migración; los índices sirven las dos consultas: por
-- módulo (qué stories tocaron un path) y por story (qué tocó una story).
CREATE TABLE IF NOT EXISTS story_files (
  project_id TEXT NOT NULL DEFAULT 'default',
  story_id   TEXT NOT NULL,
  path       TEXT NOT NULL,
  PRIMARY KEY (project_id, story_id, path)
);
CREATE INDEX IF NOT EXISTS idx_story_files_path  ON story_files(project_id, path);
CREATE INDEX IF NOT EXISTS idx_story_files_story ON story_files(project_id, story_id);
`

// migrationAddRepo is an upgrade guard that adds the repo column to existing
// databases that predate its introduction. SQLite's "duplicate column" error
// (code 1) is silently swallowed so an already-migrated DB is a no-op.
const migrationAddRepo = `ALTER TABLE stories ADD COLUMN repo TEXT NOT NULL DEFAULT ''`

// migrationAddPRURL adds the pr_url column to databases predating the merge-gated
// lifecycle. Same swallow-on-duplicate contract as migrationAddRepo.
const migrationAddPRURL = `ALTER TABLE stories ADD COLUMN pr_url TEXT NOT NULL DEFAULT ''`

// projectIDMigrations add the project_id column to stories and sprints on DBs
// predating multi-tenancy (audit A1). Same swallow-on-duplicate contract as the
// repo/pr_url migrations; existing rows are then backfilled to DefaultProjectID.
var projectIDMigrations = []string{
	`ALTER TABLE stories ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE sprints ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
}

// migrationAddExternalRef adds the import idempotency key column to DBs predating
// ticket import (v1.3). Same swallow-on-duplicate contract as the other migrations.
const migrationAddExternalRef = `ALTER TABLE stories ADD COLUMN external_ref TEXT NOT NULL DEFAULT ''`

// migrationAddKind adds the story/bug classification column to DBs predating it.
// Same swallow-on-duplicate contract; existing rows default to 'story'.
const migrationAddKind = `ALTER TABLE stories ADD COLUMN kind TEXT NOT NULL DEFAULT 'story'`

// migrationAddScreenKey adds the frontend-story→mockup column to DBs predating it
// (the store becoming the source of truth for screen_key). Same swallow-on-duplicate
// contract; existing rows default to ” (fall back to docs/backlog.yaml in the groomer).
const migrationAddScreenKey = `ALTER TABLE stories ADD COLUMN screen_key TEXT NOT NULL DEFAULT ''`

// sprintPlanningMigrations add the sprint-planning-ceremony columns to DBs predating
// them. Same swallow-on-duplicate contract as the other migrations. These MUST be
// applied AFTER migrateSprintsCompositePK: that rebuild copies an EXPLICIT column
// list (id, name, goal, project_id), so a planned_at/planning_run_id added before it
// would be silently dropped when an ancient single-PK sprints table is rebuilt.
var sprintPlanningMigrations = []string{
	`ALTER TABLE sprints ADD COLUMN planned_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE sprints ADD COLUMN planning_run_id TEXT NOT NULL DEFAULT ''`,
	// Sprint-review ceremony columns — same "AFTER migrateSprintsCompositePK"
	// contract as the planning columns above (the composite-PK rebuild copies an
	// EXPLICIT column list, so columns added before it would be silently dropped).
	`ALTER TABLE sprints ADD COLUMN reviewed_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE sprints ADD COLUMN review_run_id TEXT NOT NULL DEFAULT ''`,
	// Sprint-retrospective ceremony columns — same post-composite-PK contract.
	`ALTER TABLE sprints ADD COLUMN retro_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE sprints ADD COLUMN retro_run_id TEXT NOT NULL DEFAULT ''`,
}

// Store is the ticket store backed by a sqlite database.
type Store struct {
	db *sql.DB
	// OnStoryDone, if set, fires after a story transitions to done (story OR sprint
	// mode), with its project/story/run ids. The app wires it to billing.CountFeature
	// (a billable feature = a story that reached done). Idempotency is the callback's
	// job. This decouples tickets from billing (no import).
	OnStoryDone func(projectID, storyID, runID string)
}

// fireStoryDone notifies the OnStoryDone hook for a story that just reached done.
// projectID scopes the billing fetch to the exact story that transitioned; empty
// falls back to the legacy unscoped lookup (which can pick a same-id story from
// the wrong project when ids collide across tenants).
func (s *Store) fireStoryDone(projectID, id string) {
	if s.OnStoryDone == nil {
		return
	}
	if st, err := s.getStory(projectID, id); err == nil {
		s.OnStoryDone(st.ProjectID, st.ID, st.RunID)
	}
}

// Open opens (creating if needed) the sqlite database at path and applies the
// schema. Matches the DSN pattern used in internal/store for WAL + busy_timeout.
// For databases predating the repo column, a guarded ALTER TABLE is applied so
// an existing DB upgrades without error on restart.
func Open(path string) (*Store, error) {
	// _txlock=immediate forces BEGIN IMMEDIATE on every tx so a claimer takes the
	// write lock before any read (matching the kernel store, internal/store/store.go).
	// SetMaxOpenConns(1) serializes writers through a single connection so concurrent
	// claims queue on the busy_timeout instead of failing with "database is locked".
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Upgrade guard: add repo column to databases created before this migration.
	// SQLite returns "duplicate column name" (error text contains "duplicate column")
	// when the column already exists; that is not an error here.
	if _, err := db.Exec(migrationAddRepo); err != nil && !isDuplicateColumn(err) {
		db.Close()
		return nil, fmt.Errorf("migrate stories.repo: %w", err)
	}
	if _, err := db.Exec(migrationAddPRURL); err != nil && !isDuplicateColumn(err) {
		db.Close()
		return nil, fmt.Errorf("migrate stories.pr_url: %w", err)
	}
	// Multi-tenancy migration (audit A1): add project_id to stories+sprints and
	// backfill pre-existing rows to the default project so an existing backlog
	// keeps working. Idempotent — a duplicate-column error means already migrated.
	for _, m := range projectIDMigrations {
		if _, err := db.Exec(m); err != nil && !isDuplicateColumn(err) {
			db.Close()
			return nil, fmt.Errorf("migrate project_id: %w", err)
		}
	}
	for _, table := range []string{"stories", "sprints"} {
		if _, err := db.Exec(`UPDATE `+table+` SET project_id=? WHERE project_id=''`, DefaultProjectID); err != nil {
			db.Close()
			return nil, fmt.Errorf("backfill default project: %w", err)
		}
	}
	// Import idempotency migration (v1.3): add external_ref + its partial unique
	// index to DBs predating ticket import. Idempotent — duplicate-column/existing
	// index are no-ops.
	if _, err := db.Exec(migrationAddExternalRef); err != nil && !isDuplicateColumn(err) {
		db.Close()
		return nil, fmt.Errorf("migrate stories.external_ref: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_stories_external_ref ON stories(external_ref) WHERE external_ref != ''`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate external_ref index: %w", err)
	}
	// Story/bug classification (mid-sprint team moves): add kind to DBs predating it.
	// Idempotent — a duplicate-column error means already migrated.
	if _, err := db.Exec(migrationAddKind); err != nil && !isDuplicateColumn(err) {
		db.Close()
		return nil, fmt.Errorf("migrate stories.kind: %w", err)
	}
	// screen_key must be added BEFORE migrateToCompositePK, exactly like kind: the
	// rebuild copies rows with `SELECT *`, so the live stories table and stories_new
	// must have the same column set/order when the composite-PK migration runs.
	if _, err := db.Exec(migrationAddScreenKey); err != nil && !isDuplicateColumn(err) {
		db.Close()
		return nil, fmt.Errorf("migrate stories.screen_key: %w", err)
	}
	if err := migrateToCompositePK(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate composite pk: %w", err)
	}
	if err := migrateSprintsCompositePK(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sprints composite pk: %w", err)
	}
	// Sprint-planning ceremony columns — added AFTER the composite-PK rebuild (see the
	// sprintPlanningMigrations comment). Idempotent — a duplicate-column error means
	// already migrated (fresh DBs get the columns from the schema string above).
	for _, m := range sprintPlanningMigrations {
		if _, err := db.Exec(m); err != nil && !isDuplicateColumn(err) {
			db.Close()
			return nil, fmt.Errorf("migrate sprints planning columns: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// migrateToCompositePK upgrades the stories and story_deps tables from a
// single-column PRIMARY KEY (id) to a composite PRIMARY KEY (id, project_id)
// so stories from different projects with the same id coexist without collision.
// Idempotent: if the migration has already run (≥2 PK columns on stories),
// it returns nil immediately.
func migrateToCompositePK(db *sql.DB) error {
	// Check if already migrated: pragma_table_info returns one row per column
	// where pk > 0 for each column that is part of the primary key.
	var pkCols int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('stories') WHERE pk > 0`).Scan(&pkCols); err != nil {
		return fmt.Errorf("migrateToCompositePK: check pk cols: %w", err)
	}
	if pkCols >= 2 {
		return nil // already migrated
	}

	// Step (f) must run OUTSIDE the transaction: some SQLite drivers handle
	// ALTER TABLE ... ADD COLUMN poorly inside an explicit transaction.
	if _, err := db.Exec(`ALTER TABLE story_deps ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumn(err) {
		return fmt.Errorf("migrateToCompositePK: add story_deps.project_id: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migrateToCompositePK: begin: %w", err)
	}
	defer tx.Rollback()

	steps := []string{
		`CREATE TABLE stories_new (
			id           TEXT NOT NULL,
			epic_id      TEXT NOT NULL DEFAULT '',
			sprint_id    TEXT NOT NULL DEFAULT '',
			title        TEXT NOT NULL DEFAULT '',
			body         TEXT NOT NULL DEFAULT '',
			accept       TEXT NOT NULL DEFAULT '',
			owner        TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'backlog',
			run_id       TEXT NOT NULL DEFAULT '',
			repo         TEXT NOT NULL DEFAULT '',
			pr_url       TEXT NOT NULL DEFAULT '',
			project_id   TEXT NOT NULL DEFAULT '',
			external_ref TEXT NOT NULL DEFAULT '',
			kind         TEXT NOT NULL DEFAULT 'story',
			screen_key   TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (id, project_id)
		)`,
		// Explicit column lists on BOTH sides (root cause of #17): `SELECT *` copies by
		// POSITION, so a column added to `stories` (via an ALTER) but forgotten in
		// stories_new silently misaligns every value by one — or crashes with a bare
		// column-count mismatch. Naming the columns makes a future missing column fail
		// with a NAMED SQL error ("no such column: X") instead. Keep this list in sync
		// with the stories_new definition above and every migrationAdd* ALTER.
		`INSERT OR IGNORE INTO stories_new(id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key)
			SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key FROM stories`,
		`DROP TABLE stories`,
		`ALTER TABLE stories_new RENAME TO stories`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_stories_external_ref ON stories(external_ref) WHERE external_ref != ''`,
		`UPDATE story_deps SET project_id = (SELECT project_id FROM stories WHERE id = story_deps.story_id LIMIT 1) WHERE project_id = ''`,
		`CREATE TABLE story_deps_new (
			story_id   TEXT NOT NULL,
			dep_id     TEXT NOT NULL,
			project_id TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (story_id, dep_id, project_id)
		)`,
		`INSERT OR IGNORE INTO story_deps_new SELECT story_id, dep_id, project_id FROM story_deps`,
		`DROP TABLE story_deps`,
		`ALTER TABLE story_deps_new RENAME TO story_deps`,
		`CREATE INDEX IF NOT EXISTS idx_story_deps_story ON story_deps(story_id, project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_story_deps_dep ON story_deps(dep_id, project_id)`,
	}

	for _, step := range steps {
		if _, err := tx.Exec(step); err != nil {
			return fmt.Errorf("migrateToCompositePK: %w", err)
		}
	}
	return tx.Commit()
}

// migrateSprintsCompositePK upgrades the sprints table from a single-column
// PRIMARY KEY (id) to a composite PRIMARY KEY (id, project_id) so sprints from
// different projects with the same id coexist without collision. Idempotent.
func migrateSprintsCompositePK(db *sql.DB) error {
	var pkCols int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sprints') WHERE pk > 0`).Scan(&pkCols); err != nil {
		return fmt.Errorf("migrateSprintsCompositePK: check pk cols: %w", err)
	}
	if pkCols >= 2 {
		return nil // already migrated
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migrateSprintsCompositePK: begin: %w", err)
	}
	defer tx.Rollback()

	steps := []string{
		`CREATE TABLE sprints_new (
			id         TEXT NOT NULL,
			name       TEXT NOT NULL DEFAULT '',
			goal       TEXT NOT NULL DEFAULT '',
			project_id TEXT NOT NULL DEFAULT 'default',
			PRIMARY KEY (id, project_id)
		)`,
		`INSERT OR IGNORE INTO sprints_new SELECT id, name, goal, project_id FROM sprints`,
		`DROP TABLE sprints`,
		`ALTER TABLE sprints_new RENAME TO sprints`,
	}
	for _, step := range steps {
		if _, err := tx.Exec(step); err != nil {
			return fmt.Errorf("migrateSprintsCompositePK: %w", err)
		}
	}
	return tx.Commit()
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// ---- Epics ------------------------------------------------------------------

// CreateEpic inserts an Epic. Returns ErrNotFound if id is empty.
func (s *Store) CreateEpic(e Epic) error {
	if e.ID == "" {
		return errors.New("epic id is required")
	}
	_, err := s.db.Exec(`INSERT INTO epics(id, title, description) VALUES(?,?,?)`,
		e.ID, e.Title, e.Description)
	return err
}

// GetEpic loads an Epic by id.
func (s *Store) GetEpic(id string) (Epic, error) {
	var e Epic
	err := s.db.QueryRow(`SELECT id, title, description FROM epics WHERE id=?`, id).
		Scan(&e.ID, &e.Title, &e.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return Epic{}, ErrNotFound
	}
	return e, err
}

// ListEpics returns all epics ordered by id.
func (s *Store) ListEpics() ([]Epic, error) {
	rows, err := s.db.Query(`SELECT id, title, description FROM epics ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Epic
	for rows.Next() {
		var e Epic
		if err := rows.Scan(&e.ID, &e.Title, &e.Description); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EpicProjects maps each story-referenced epic id to the projects of those
// stories. Epics predate multi-tenancy (no project_id column), so their tenant
// is DERIVED from the stories that reference them — the API uses this to scope
// epic reads per user (C1). An epic no story references maps to nothing.
func (s *Store) EpicProjects() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT epic_id, project_id FROM stories WHERE epic_id != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var epicID, projectID string
		if err := rows.Scan(&epicID, &projectID); err != nil {
			return nil, err
		}
		out[epicID] = append(out[epicID], projectID)
	}
	return out, rows.Err()
}

// ---- Sprints ----------------------------------------------------------------

// CreateSprint inserts a Sprint.
func (s *Store) CreateSprint(sp Sprint) error {
	if sp.ID == "" {
		return errors.New("sprint id is required")
	}
	if sp.ProjectID == "" {
		sp.ProjectID = DefaultProjectID // back-compat: an unscoped sprint joins the default project
	}
	_, err := s.db.Exec(`INSERT INTO sprints(id, name, goal, project_id) VALUES(?,?,?,?)`,
		sp.ID, sp.Name, sp.Goal, sp.ProjectID)
	return err
}

// SetSprintPlanningRun records the sprint-planning run the scheduler started for a
// sprint — the persisted idempotency reference that keeps a second planning run from
// starting while this one is live. projectID empty defaults to the default project.
// Returns ErrNotFound when the sprint does not exist.
func (s *Store) SetSprintPlanningRun(sprintID, projectID, runID string) error {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	res, err := s.db.Exec(`UPDATE sprints SET planning_run_id=? WHERE id=? AND project_id=?`, runID, sprintID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSprintPlanned stamps a sprint's planned_at with the current time — the signal
// the ceremony plan was approved and applied, which unblocks the scheduler from
// firing the sprint. projectID empty defaults to the default project. Returns
// ErrNotFound when the sprint does not exist.
func (s *Store) SetSprintPlanned(sprintID, projectID string) error {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	res, err := s.db.Exec(`UPDATE sprints SET planned_at=? WHERE id=? AND project_id=?`, time.Now().UnixMilli(), sprintID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSprintReviewRun records the sprint-review run the scheduler started for a
// sprint — the persisted idempotency reference (twin of SetSprintPlanningRun) that
// keeps a second review run from starting while this one is live. projectID empty
// defaults to the default project. Returns ErrNotFound when the sprint does not exist.
func (s *Store) SetSprintReviewRun(sprintID, projectID, runID string) error {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	res, err := s.db.Exec(`UPDATE sprints SET review_run_id=? WHERE id=? AND project_id=?`, runID, sprintID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSprintReviewed stamps a sprint's reviewed_at with the current time — the signal
// the ceremony accepted this increment, which unblocks the scheduler from advancing
// the project to the next sprint. projectID empty defaults to the default project.
// Returns ErrNotFound when the sprint does not exist.
func (s *Store) SetSprintReviewed(sprintID, projectID string) error {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	res, err := s.db.Exec(`UPDATE sprints SET reviewed_at=? WHERE id=? AND project_id=?`, time.Now().UnixMilli(), sprintID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSprintRetroRun records the sprint-retro run the scheduler started for a sprint —
// the persisted idempotency reference (twin of SetSprintReviewRun). projectID empty
// defaults to the default project. Returns ErrNotFound when the sprint does not exist.
func (s *Store) SetSprintRetroRun(sprintID, projectID, runID string) error {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	res, err := s.db.Exec(`UPDATE sprints SET retro_run_id=? WHERE id=? AND project_id=?`, runID, sprintID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSprintRetroed stamps a sprint's retro_at with the current time — the signal the
// retrospective applied its method proposals, which unblocks the scheduler from
// advancing the project to the next sprint. projectID empty defaults to the default
// project. Returns ErrNotFound when the sprint does not exist.
func (s *Store) SetSprintRetroed(sprintID, projectID string) error {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	res, err := s.db.Exec(`UPDATE sprints SET retro_at=? WHERE id=? AND project_id=?`, time.Now().UnixMilli(), sprintID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReviewedSprintsAwaitingRetro returns the sprints that have been reviewed
// (reviewed_at != 0) but not yet retrospected (retro_at == 0). projectID empty = all
// projects (the scheduler's global sweep). Gating on reviewed_at (not done-ness)
// enforces the ceremony order review(N) → retro(N): a sprint reaches this set only
// after review_close stamps reviewed_at.
func (s *Store) ReviewedSprintsAwaitingRetro(projectID string) ([]Sprint, error) {
	sprints, err := s.listSprints(projectID)
	if err != nil {
		return nil, err
	}
	var out []Sprint
	for _, sp := range sprints {
		if sp.ReviewedAt != 0 && sp.RetroAt == 0 {
			out = append(out, sp)
		}
	}
	return out, nil
}

// DoneSprintsAwaitingReview returns the sprints whose increment is complete but not
// yet accepted by the sprint-review ceremony: every story done (≥1 story) and
// reviewed_at == 0. projectID empty = all projects (the scheduler's global sweep).
// A sprint is "done" only when ALL its stories are done — a failed/mixed sprint is
// not an accepted increment, so it is not offered for review.
func (s *Store) DoneSprintsAwaitingReview(projectID string) ([]Sprint, error) {
	sprints, err := s.listSprints(projectID)
	if err != nil {
		return nil, err
	}
	var out []Sprint
	for _, sp := range sprints {
		if sp.ReviewedAt != 0 {
			continue
		}
		done, err := s.sprintAllDone(sp.ID, sp.ProjectID)
		if err != nil {
			return nil, err
		}
		if done {
			out = append(out, sp)
		}
	}
	return out, nil
}

// sprintAllDone reports whether the sprint has ≥1 story and every story is done —
// the completed-increment predicate DoneSprintsAwaitingReview gates on.
func (s *Store) sprintAllDone(sprintID, projectID string) (bool, error) {
	stories, err := s.storiesBySprint(sprintID, projectID)
	if err != nil {
		return false, err
	}
	if len(stories) == 0 {
		return false, nil
	}
	for _, st := range stories {
		if st.Status != StatusDone {
			return false, nil
		}
	}
	return true, nil
}

// ListSprints returns all sprints ordered by id.
func (s *Store) ListSprints() ([]Sprint, error) {
	return s.listSprints("")
}

// ListSprintsByProject returns the sprints of one project (empty = all
// projects, for the service token / back-compat), ordered by id.
func (s *Store) ListSprintsByProject(projectID string) ([]Sprint, error) {
	return s.listSprints(projectID)
}

// SprintProjects returns the ids of every project containing a sprint with
// this id. Sprints are keyed (id, project_id) and the per-id mutators act
// across projects, so the API authorizes a sprint mutation against EVERY
// project returned here (C1).
func (s *Store) SprintProjects(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT project_id FROM sprints WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}

// listSprints returns sprints, optionally scoped to projectID (empty = all
// projects, for admin/back-compat), ordered by id.
func (s *Store) listSprints(projectID string) ([]Sprint, error) {
	q := `SELECT id, name, goal, project_id, planned_at, planning_run_id, reviewed_at, review_run_id, retro_at, retro_run_id FROM sprints`
	var args []any
	if projectID != "" {
		q += ` WHERE project_id=?`
		args = append(args, projectID)
	}
	q += ` ORDER BY id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sprint
	for rows.Next() {
		var sp Sprint
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.Goal, &sp.ProjectID, &sp.PlannedAt, &sp.PlanningRunID, &sp.ReviewedAt, &sp.ReviewRunID, &sp.RetroAt, &sp.RetroRunID); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ---- Stories ----------------------------------------------------------------

// CreateStory inserts a Story and its deps into story_deps.
func (s *Store) CreateStory(st Story) error {
	if st.ID == "" {
		return errors.New("story id is required")
	}
	if st.Status == "" {
		st.Status = StatusBacklog
	}
	if st.ProjectID == "" {
		st.ProjectID = DefaultProjectID // back-compat: an unscoped story joins the default project
	}
	if st.Kind == "" {
		st.Kind = KindStory // default classification
	}
	if !validKind(st.Kind) {
		return fmt.Errorf("invalid kind %q: want %q or %q", st.Kind, KindStory, KindBug)
	}
	// Reject a self-dependency up front: it never resolves (a story can't be its own
	// "done" prerequisite) and would deadlock readiness forever (H7).
	for _, dep := range st.Deps {
		if dep == st.ID {
			return fmt.Errorf("%w: %s depends on itself", ErrDepCycle, st.ID)
		}
	}
	// Cycle check against the CURRENT store graph plus this story's new edges. Deps
	// pointing at stories not yet created (a forward reference in the same publish
	// batch) are left for publish-time whole-graph validation (H6); an edge to an
	// existing story that closes a cycle is rejected here (H7).
	if err := s.checkNoCycle(st.ID, st.Deps, st.ProjectID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO stories(id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		st.ID, st.EpicID, st.SprintID, st.Title, st.Body, st.Accept, st.Owner, string(st.Status), st.RunID, st.Repo, st.PRURL, st.ProjectID, st.ExternalRef, st.Kind, st.ScreenKey)
	if err != nil {
		return err
	}
	for _, dep := range st.Deps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id, project_id) VALUES(?,?,?)`, st.ID, dep, st.ProjectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StoryIDByExternalRef returns the id of the story carrying externalRef, and false
// if none does. The idempotency lookup for import (v1.3): an importer checks this
// before creating, so re-importing the same external issue is a no-op.
func (s *Store) StoryIDByExternalRef(externalRef string) (string, bool, error) {
	if externalRef == "" {
		return "", false, nil
	}
	var id string
	err := s.db.QueryRow(`SELECT id FROM stories WHERE external_ref=?`, externalRef).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// SetStoryExternalRef records the external mirror of a story (e.g.
// "github:owner/repo#N" after the backlog export), making re-exports idempotent —
// the exporter skips stories that already carry a ref. The write is guarded so a
// story never silently flips from one external issue to another.
func (s *Store) SetStoryExternalRef(projectID, id, externalRef string) error {
	q := `UPDATE stories SET external_ref=? WHERE id=? AND (external_ref IS NULL OR external_ref='' OR external_ref=?)`
	args := []any{externalRef, id, externalRef}
	if projectID != "" {
		// Scope to the project: external_ref is UNIQUE globally, so a bare `WHERE id=?`
		// would try to stamp BOTH a same-id story in another project AND this one with
		// the same ref, tripping the unique constraint (the S11-01 export failure).
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("story %s: not found or already linked to a different external ref", id)
	}
	return nil
}

// StoryDependents returns the ids of stories in projectID that declare id as a
// dependency (a story_deps row with dep_id = id). A non-empty result means deleting
// id would orphan those dependents' edges — the API blocks the delete (409) and
// lists them so the user removes the edges first.
func (s *Store) StoryDependents(projectID, id string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT story_id FROM story_deps WHERE dep_id=? AND project_id=? ORDER BY story_id ASC`, id, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, rows.Err()
}

// DeleteStory removes a story and its side rows within projectID: its dep edges in
// BOTH directions (as story_id and as dep_id) and its file links. It does NOT touch
// GitHub — a story mirrored as an issue (external_ref) is deleted locally only; the
// caller surfaces the surviving issue to the user. Callers MUST check StoryDependents
// first (the API returns 409 when non-empty). Scoped by the composite key so a
// same-id story in another tenant is never deleted. Returns ErrNotFound if absent.
func (s *Store) DeleteStory(projectID, id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM stories WHERE id=? AND project_id=?`, id, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM story_deps WHERE (story_id=? OR dep_id=?) AND project_id=?`, id, id, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM story_files WHERE story_id=? AND project_id=?`, id, projectID); err != nil {
		return err
	}
	// story_sessions is keyed by story_id alone (no project column); drop the link so
	// the deleted story leaves no dangling agent-session pointer.
	if _, err := tx.Exec(`DELETE FROM story_sessions WHERE story_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetStorySession records the GitHub agent session executing a dispatched story
// (Copilot task URL / Actions run URL) so the console can link straight to it.
func (s *Store) SetStorySession(id, url string) error {
	_, err := s.db.Exec(
		`INSERT INTO story_sessions(story_id, url) VALUES(?, ?)
		 ON CONFLICT(story_id) DO UPDATE SET url=excluded.url`, id, url)
	return err
}

// SessionURLs returns story_id → session URL for a project's stories (all
// projects when projectID is empty, matching ListStories' scoping contract).
func (s *Store) SessionURLs(projectID string) (map[string]string, error) {
	q := `SELECT ss.story_id, ss.url FROM story_sessions ss
	      JOIN stories st ON st.id = ss.story_id`
	var args []any
	if projectID != "" {
		q += ` WHERE st.project_id = ?`
		args = append(args, projectID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, url string
		if err := rows.Scan(&id, &url); err != nil {
			return nil, err
		}
		out[id] = url
	}
	return out, rows.Err()
}

// SyncExternalStatus mirrors a story's status from its external source of truth
// (the GitHub issue, F1 projection). It deliberately BYPASSES legalSources: that
// table protects the KERNEL's execution flow, but a mirrored story is driven by
// GitHub (an issue can reopen: done→backlog; an agent can unassign:
// running→backlog). The guard is different here: only stories that actually
// carry an external_ref may be written this way, so the kernel flow is untouched.
// prURL overwrites when non-empty and is cleared when the story leaves
// in_review/running back to backlog (the PR is gone).
func (s *Store) SyncExternalStatus(id string, status Status, prURL string) (changed bool, err error) {
	var prev, prevPR string
	err = s.db.QueryRow(
		`SELECT status, pr_url FROM stories WHERE id=? AND external_ref IS NOT NULL AND external_ref!=''`, id).
		Scan(&prev, &prevPR)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // not mirrored — never touched by the projection
	}
	if err != nil {
		return false, err
	}
	newPR := prevPR
	switch {
	case prURL != "":
		newPR = prURL
	case status == StatusBacklog:
		newPR = "" // el PR que hubiera ya no aplica
	}
	statusChanged := prev != string(status)
	if !statusChanged && newPR == prevPR {
		return false, nil
	}
	if _, err := s.db.Exec(`UPDATE stories SET status=?, pr_url=? WHERE id=?`, string(status), newPR, id); err != nil {
		return false, err
	}
	if statusChanged && status == StatusBacklog {
		// De vuelta al backlog: la sesión de agente (si la hubo) ya no ejecuta nada.
		_, _ = s.db.Exec(`DELETE FROM story_sessions WHERE story_id=?`, id)
	}
	if statusChanged && status == StatusDone {
		s.fireStoryDone("", id)
	}
	return true, nil
}

// GetStory loads a Story by id, including its deps. LEGACY (unscoped): with a
// composite (id, project_id) key, a bare id can match a same-id story in the WRONG
// project (the S11-01 cross-tenant incident). Kept for the orchestrator/services
// that call by id without a project; API handlers that know the project MUST use
// GetStoryInProject so they resolve — and authorize/mutate — the right row.
func (s *Store) GetStory(id string) (Story, error) {
	return s.getStory("", id)
}

// GetStoryInProject loads a Story by its composite key (project_id, id). This is
// the correct lookup whenever the caller knows the project: it can never resolve a
// same-id story from another tenant. An empty projectID falls back to the legacy
// unscoped lookup (GetStory).
func (s *Store) GetStoryInProject(projectID, id string) (Story, error) {
	return s.getStory(projectID, id)
}

// getStory is the shared loader: scoped to projectID when non-empty, unscoped
// otherwise. It loads the story plus its deps.
func (s *Store) getStory(projectID, id string) (Story, error) {
	q := `SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key
		FROM stories WHERE id=?`
	args := []any{id}
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	q += ` LIMIT 1`
	var st Story
	err := s.db.QueryRow(q, args...).
		Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL, &st.ProjectID, &st.ExternalRef, &st.Kind, &st.ScreenKey)
	if errors.Is(err, sql.ErrNoRows) {
		return Story{}, ErrNotFound
	}
	if err != nil {
		return Story{}, err
	}
	deps, err := s.loadDeps(id, st.ProjectID)
	if err != nil {
		return Story{}, err
	}
	st.Deps = deps
	return st, nil
}

// ListStories returns all stories with their deps. To scope to one project use
// ListStoriesByProject.
func (s *Store) ListStories() ([]Story, error) {
	return s.ListStoriesByProject("")
}

// ListStoriesByProject returns stories scoped to projectID (empty = all projects,
// for admin/back-compat) with their deps, ordered by id (audit A1).
func (s *Store) ListStoriesByProject(projectID string) ([]Story, error) {
	q := `SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key
		FROM stories`
	var args []any
	if projectID != "" {
		q += ` WHERE project_id=?`
		args = append(args, projectID)
	}
	q += ` ORDER BY id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL, &st.ProjectID, &st.ExternalRef, &st.Kind, &st.ScreenKey); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		deps, err := s.loadDeps(out[i].ID, out[i].ProjectID)
		if err != nil {
			return nil, err
		}
		out[i].Deps = deps
	}
	return out, nil
}

// transition performs a guarded status change: it moves story id to target only
// if its current stored status is one of the legal sources for that target. It
// returns ErrNotFound when the id does not exist, ErrIllegalTransition when the id
// exists but its current state is not a legal source (a no-op — terminal states are
// never resurrected, illegal jumps never applied), and nil on a successful 1-row
// update. extraSet applies additional column assignments (e.g. pr_url) atomically
// with the status flip. Distinguishing "missing" from "illegal" requires a probe
// read, done only when the guarded UPDATE affects 0 rows.
func (s *Store) transition(id string, target Status, extraSet string, extraArgs ...any) error {
	return s.transitionScoped("", id, target, extraSet, extraArgs...)
}

// transitionScoped is transition scoped to projectID (empty = legacy unscoped). The
// scope is threaded onto the guarded UPDATE's WHERE so a same-id story in another
// project is never touched — critical because a bare `WHERE id=?` UPDATE would hit
// EVERY project's row sharing that id.
func (s *Store) transitionScoped(projectID, id string, target Status, extraSet string, extraArgs ...any) error {
	sources, ok := legalSources[target]
	if !ok {
		return fmt.Errorf("%w: no legal sources for target %q", ErrIllegalTransition, target)
	}
	set := "status=?"
	args := []any{string(target)}
	if extraSet != "" {
		set += ", " + extraSet
		args = append(args, extraArgs...)
	}
	args = append(args, id)
	where := "id=?"
	if projectID != "" {
		where += " AND project_id=?"
		args = append(args, projectID)
	}
	q := fmt.Sprintf(`UPDATE stories SET %s WHERE %s AND %s`, set, where, inClause(sources))
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		// Billing: a story reaching done is a billable feature. transition() is the
		// canonical per-story path — it catches BOTH MarkDone and the HTTP status
		// endpoint's UpdateStoryStatus(done) (the real factory path). Sprint-mode's
		// bulk update bypasses transition() and fires the hook itself.
		if target == StatusDone {
			s.fireStoryDone(projectID, id)
		}
		return nil
	}
	// 0 rows: either the id is missing or its current state is an illegal source.
	cur, err := s.statusOfScoped(projectID, id)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s %s→%s", ErrIllegalTransition, id, cur, target)
}

// UpdateStoryStatus sets the status column of a Story via the guarded state
// machine. Does not affect deps. It is the provider-facing generic setter; it
// rejects illegal transitions (e.g. resurrecting a terminal story) with
// ErrIllegalTransition rather than blindly overwriting the column.
//
// Two cases are handled specially so the generic setter stays usable by the HTTP
// status endpoint without it knowing the transition vocabulary:
//   - target == current status: an idempotent no-op success. The orchestrator's
//     MarkRunning re-asserts status=running on an already-running (just-claimed)
//     story purely to attach the run_id; that must not error.
//   - target == backlog: routes to MarkBacklog (the compensating reset, B3) — only
//     a claimed-but-unfired story (running, empty run_id) is reset; everything else
//     is a no-op illegal transition.
func (s *Store) UpdateStoryStatus(id string, status Status) error {
	return s.UpdateStoryStatusInProject("", id, status)
}

// UpdateStoryStatusInProject is UpdateStoryStatus scoped to projectID (empty =
// legacy unscoped). API handlers pass the authorized story's project so the flip
// only ever touches that tenant's row.
func (s *Store) UpdateStoryStatusInProject(projectID, id string, status Status) error {
	cur, err := s.statusOfScoped(projectID, id)
	if err != nil {
		return err
	}
	if cur == status {
		return nil // idempotent: already in the requested state
	}
	if status == StatusBacklog {
		return s.markBacklog(projectID, id)
	}
	return s.transitionScoped(projectID, id, status, "")
}

// statusOfScoped returns a story's current stored status, scoped to projectID when
// non-empty, or ErrNotFound.
func (s *Store) statusOfScoped(projectID, id string) (Status, error) {
	q := `SELECT status FROM stories WHERE id=?`
	args := []any{id}
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	var cur Status
	err := s.db.QueryRow(q, args...).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return cur, nil
}

// ClaimStory atomically transitions a story from backlog → running.
// Returns true if this caller claimed it, false if it was already taken.
func (s *Store) ClaimStory(id string) (bool, error) {
	return s.ClaimStoryInProject("", id)
}

// ClaimStoryInProject is ClaimStory scoped to projectID (empty = legacy unscoped),
// so a claim only ever moves the addressed tenant's row backlog → running.
func (s *Store) ClaimStoryInProject(projectID, id string) (bool, error) {
	q := `UPDATE stories SET status='running' WHERE id=? AND status='backlog'`
	args := []any{id}
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkBacklog resets a claimed-but-not-fired story from running back to backlog so
// it becomes Ready again next cycle. It is the compensating action for a FireRun
// that failed AFTER a successful claim (B3): the story is running with an empty
// run_id and would otherwise be stranded forever (the completion loop skips
// empty-run_id stories). Only a running story with NO recorded run_id is reset —
// a running story that already has a run_id is genuinely executing and must not be
// clawed back. Returns ErrIllegalTransition (no-op) if the story is not in that
// reset-eligible state, ErrNotFound if it does not exist.
func (s *Store) MarkBacklog(id string) error {
	return s.markBacklog("", id)
}

// markBacklog is MarkBacklog scoped to projectID (empty = legacy unscoped).
func (s *Store) markBacklog(projectID, id string) error {
	q := `UPDATE stories SET status='backlog', run_id='' WHERE id=? AND status='running' AND run_id=''`
	args := []any{id}
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	cur, err := s.statusOfScoped(projectID, id)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s reset-claim from %s (run_id may be set)", ErrIllegalTransition, id, cur)
}

// MarkSprintBacklog resets a sprint's just-claimed stories (running, empty run_id)
// back to backlog — the sprint-wide compensating action for a FireRun that failed
// after ClaimSprint (B3). Stories that already carry a run_id are left running.
func (s *Store) MarkSprintBacklog(sprintID string) error {
	_, err := s.db.Exec(
		`UPDATE stories SET status='backlog' WHERE sprint_id=? AND status='running' AND run_id=''`, sprintID)
	return err
}

// MarkFailed sets a story's status to failed. Used when the run driving it
// reaches a terminal non-DONE state (FAILED, CANCELLED) so the story is not
// stuck in "running" forever. Guarded: a failure may strike any non-terminal
// state (backlog/running/in_review) but never re-fails a done/failed story.
func (s *Store) MarkFailed(id string) error {
	return s.transition(id, StatusFailed, "")
}

// RequeueSprint resurrects a whole sprint's terminal (failed) stories back to
// backlog so the orchestrator re-fires the sprint fresh. This is the user-driven
// "Reencolar" action (R2): it deliberately crosses the failed->backlog edge the
// automatic state machine forbids, and clears run_id/pr_url so the next fire is
// clean. Returns the number of stories requeued.
func (s *Store) RequeueSprint(sprintID string) (int, error) {
	res, err := s.db.Exec(
		`UPDATE stories SET status='backlog', run_id='', pr_url='' WHERE sprint_id=? AND status='failed'`, sprintID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RequeueStory resurrects ONE terminal (failed) story back to backlog, clearing
// run_id/pr_url so the next dispatch fires clean. Like RequeueSprint it crosses the
// failed→backlog edge the automatic state machine forbids, but scoped to a single id
// (and project when non-empty). It is guarded on status='failed', so it is a safe
// no-op (changed=false) if the story already moved out of failed between the caller's
// read and this write — never blindly overwriting a running/in_review/done story.
// For GitHub-mirrored stories the handler uses SyncExternalStatus instead (it also
// clears the agent session); this path serves legacy, non-mirrored stories.
func (s *Store) RequeueStory(projectID, id string) (changed bool, err error) {
	q := `UPDATE stories SET status='backlog', run_id='', pr_url='' WHERE id=? AND status='failed'`
	args := []any{id}
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RequeueByRun requeues the failed stories belonging to a run. Because a goal-mode
// sprint's stories all share the run_id, it resolves each story's sprint and
// requeues the WHOLE sprint (the user's "requeue the whole sprint in sprint mode");
// story-mode stories (no sprint_id) are requeued individually by run_id. Returns
// the total number of stories requeued.
func (s *Store) RequeueByRun(runID string) (int, error) {
	rows, err := s.db.Query(`SELECT DISTINCT sprint_id FROM stories WHERE run_id=?`, runID)
	if err != nil {
		return 0, err
	}
	var sprints []string
	looseStory := false
	for rows.Next() {
		var sp string
		if err := rows.Scan(&sp); err != nil {
			rows.Close()
			return 0, err
		}
		if sp == "" {
			looseStory = true
		} else {
			sprints = append(sprints, sp)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, sp := range sprints {
		n, err := s.RequeueSprint(sp)
		if err != nil {
			return total, err
		}
		total += n
	}
	if looseStory {
		res, err := s.db.Exec(
			`UPDATE stories SET status='backlog', run_id='', pr_url='' WHERE run_id=? AND sprint_id='' AND status='failed'`, runID)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

// ---- Human "team-move" operations (mid-sprint) -----------------------------
// These model the moves a real team makes on the board between sprints: cancel a
// story, move it to another sprint, split it into smaller ones, and edit its
// fields/deps. All are operator-driven (never automatic) and each is guarded on the
// story's current state so a live run is never corrupted. They mirror Requeue's
// transactional, guarded-UPDATE discipline.

// CancelStory moves a story to the terminal `cancelled` state through the guarded
// state machine. Legal only from backlog/failed/in_review (legalSources[cancelled]);
// a `running` story is refused with ErrIllegalTransition — cancel its run first (which
// parks the story failed, from where cancel is legal). Missing id → ErrNotFound.
// Scoped to projectID (empty = legacy unscoped).
func (s *Store) CancelStory(projectID, id string) error {
	return s.transitionScoped(projectID, id, StatusCancelled, "")
}

// MoveStory reassigns a story to newSprintID (empty = the loose/no-sprint pool).
// Allowed only while the story is planning-mutable (backlog or failed); a
// running/in_review/terminal story is refused (ErrInvalidState). The move must not
// break the ordering the dependency graph implies: the target sprint may sit no
// EARLIER than any sprint the story depends on, and no LATER than any sprint that
// depends on the story (ErrDepOrder). Sprint order is the id-ascending order the
// scheduler fires them in. A non-empty target sprint must exist in the story's
// project (else ErrNotFound). projectID empty resolves the story's own project.
func (s *Store) MoveStory(projectID, id, newSprintID string) error {
	st, err := s.getStory(projectID, id)
	if err != nil {
		return err
	}
	if st.Status != StatusBacklog && st.Status != StatusFailed {
		return fmt.Errorf("%w: %s is %s (move allowed only from backlog/failed)", ErrInvalidState, id, st.Status)
	}
	if newSprintID != "" {
		ok, err := s.sprintExists(newSprintID, st.ProjectID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: sprint %s", ErrNotFound, newSprintID)
		}
	}
	if err := s.checkMoveOrdering(st, newSprintID); err != nil {
		return err
	}
	// Guarded on backlog/failed so a story that raced into running between our read and
	// this write is not silently moved from under its run.
	q := `UPDATE stories SET sprint_id=? WHERE id=? AND ` + inClause([]Status{StatusBacklog, StatusFailed})
	args := []any{newSprintID, id}
	if st.ProjectID != "" {
		q += ` AND project_id=?`
		args = append(args, st.ProjectID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s changed state during move", ErrInvalidState, id)
	}
	return nil
}

// checkMoveOrdering verifies placing story st in targetSprint respects the sprint
// order its dependency edges imply. A sprint's rank is its index in id-ascending order
// (the fire order); a lower rank is "earlier". For every dep b of st it requires
// rank(target) >= rank(b.sprint); for every story c that depends on st it requires
// rank(c.sprint) >= rank(target). Deps/dependents with no sprint (loose) impose no
// ordering and are skipped, as are sprints that no longer exist. Moving to the loose
// pool (target "") is unranked and thus unconstrained. Returns ErrDepOrder naming the
// first offending edge, or nil.
func (s *Store) checkMoveOrdering(st Story, targetSprint string) error {
	ranks, err := s.sprintRanks(st.ProjectID)
	if err != nil {
		return err
	}
	tRank, tOK := ranks[targetSprint]
	if !tOK {
		return nil // loose target: no fire-order position to violate
	}
	for _, dep := range st.Deps {
		sp, err := s.sprintOf(dep, st.ProjectID)
		if err != nil {
			return err
		}
		if r, ok := ranks[sp]; ok && tRank < r {
			return fmt.Errorf("%w: %s depends on %s which is in a later sprint (%s)", ErrDepOrder, st.ID, dep, sp)
		}
	}
	dependents, err := s.StoryDependents(st.ProjectID, st.ID)
	if err != nil {
		return err
	}
	for _, c := range dependents {
		sp, err := s.sprintOf(c, st.ProjectID)
		if err != nil {
			return err
		}
		if r, ok := ranks[sp]; ok && tRank > r {
			return fmt.Errorf("%w: %s is depended on by %s which is in an earlier sprint (%s)", ErrDepOrder, st.ID, c, sp)
		}
	}
	return nil
}

// sprintRanks maps each sprint id to its position in id-ascending order — the order the
// scheduler fires sprints in — so a lower value means "earlier".
func (s *Store) sprintRanks(projectID string) (map[string]int, error) {
	sprints, err := s.listSprints(projectID)
	if err != nil {
		return nil, err
	}
	ranks := make(map[string]int, len(sprints))
	for i, sp := range sprints {
		ranks[sp.ID] = i
	}
	return ranks, nil
}

// sprintExists reports whether a sprint id exists in projectID.
func (s *Store) sprintExists(id, projectID string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM sprints WHERE id=? AND project_id=?`, id, projectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// sprintOf returns a story's sprint_id ("" if loose or the story is absent). A missing
// story yields "" with no error: a dangling edge imposes no sprint ordering (the
// dangling-dep integrity concern is surfaced elsewhere, D7).
func (s *Store) sprintOf(id, projectID string) (string, error) {
	var sp string
	err := s.db.QueryRow(`SELECT sprint_id FROM stories WHERE id=? AND project_id=?`, id, projectID).Scan(&sp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return sp, nil
}

// SplitStory replaces one story with N new ones (parts). Each part inherits the
// original's epic, sprint, kind, repo, and dependency edges; the part's own
// title/body/acceptance/owner come from the draft (owner falls back to the original's).
// Every story that depended on the original is REWIRED to depend on all parts instead,
// so the split leaves no dangling dependency (D7) — dependents still wait for the work,
// now spread across the parts. The original is marked `cancelled` with a note recording
// the parts. Allowed only from backlog/failed (ErrInvalidState); needs ≥2 parts.
// All-or-nothing in one transaction. Returns the new ids in order.
//
// The rewrite is acyclic by construction: parts depend only on the original's deps
// (which could not reach the original in an acyclic graph), so no dependent→part edge
// closes a loop — hence no cycle re-validation is needed.
func (s *Store) SplitStory(projectID, id string, parts []StoryDraft) ([]string, error) {
	if len(parts) < 2 {
		return nil, fmt.Errorf("%w: split needs at least 2 parts, got %d", ErrInvalidInput, len(parts))
	}
	orig, err := s.getStory(projectID, id)
	if err != nil {
		return nil, err
	}
	if orig.Status != StatusBacklog && orig.Status != StatusFailed {
		return nil, fmt.Errorf("%w: %s is %s (split allowed only from backlog/failed)", ErrInvalidState, id, orig.Status)
	}
	// Resolve part ids: an explicit draft id wins, an empty one gets a deterministic
	// suffix (<id>-a, <id>-b, …). Each must be new (no collision) and unique in the batch.
	ids := make([]string, len(parts))
	seen := map[string]bool{}
	for i := range parts {
		pid := strings.TrimSpace(parts[i].ID)
		if pid == "" {
			pid = fmt.Sprintf("%s-%c", id, 'a'+i)
		}
		if pid == id {
			return nil, fmt.Errorf("%w: split part id %q collides with the original", ErrInvalidInput, pid)
		}
		if seen[pid] {
			return nil, fmt.Errorf("%w: duplicate split part id %q", ErrInvalidInput, pid)
		}
		exists, err := s.storyExists(pid, orig.ProjectID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf("%w: split part id %q already exists", ErrInvalidInput, pid)
		}
		seen[pid] = true
		ids[i] = pid
	}
	dependents, err := s.StoryDependents(orig.ProjectID, id)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	for i, pid := range ids {
		owner := parts[i].Owner
		if owner == "" {
			owner = orig.Owner
		}
		if _, err := tx.Exec(
			`INSERT INTO stories(id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			pid, orig.EpicID, orig.SprintID, parts[i].Title, parts[i].Body, parts[i].Accept, owner,
			string(StatusBacklog), "", orig.Repo, "", orig.ProjectID, "", orig.Kind, orig.ScreenKey); err != nil {
			return nil, err
		}
		for _, dep := range orig.Deps {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id, project_id) VALUES(?,?,?)`, pid, dep, orig.ProjectID); err != nil {
				return nil, err
			}
		}
	}
	// Rewire each dependent off the original and onto every part.
	for _, c := range dependents {
		if _, err := tx.Exec(`DELETE FROM story_deps WHERE story_id=? AND dep_id=? AND project_id=?`, c, id, orig.ProjectID); err != nil {
			return nil, err
		}
		for _, pid := range ids {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id, project_id) VALUES(?,?,?)`, c, pid, orig.ProjectID); err != nil {
				return nil, err
			}
		}
	}
	// Cancel the original (guarded on backlog/failed) and stamp the split note. `body||?`
	// appends the note in-place so the record explains why it was cancelled.
	note := "\n\n_Split into: " + strings.Join(ids, ", ") + "_"
	res, err := tx.Exec(
		`UPDATE stories SET status='cancelled', body=body||? WHERE id=? AND project_id=? AND `+inClause([]Status{StatusBacklog, StatusFailed}),
		note, id, orig.ProjectID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("%w: %s changed state during split", ErrInvalidState, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// editable reports whether a story's fields/deps may be edited in its current state:
// the planning-mutable states (backlog/failed/in_review). A running story is refused
// (its run is live); done and cancelled are terminal and immutable.
func editable(st Status) bool {
	return st == StatusBacklog || st == StatusFailed || st == StatusInReview
}

// EditStory applies a sparse patch (title/body/acceptance/owner/screen_key/deps) to a story.
// Allowed only in an editable state (backlog/failed/in_review) — never on a running,
// done, or cancelled story (ErrInvalidState). When patch.Deps is non-nil it REPLACES
// the dependency set and revalidates the graph the same way publish/AddDep do (no
// self-dep, every dep must exist, no cycle). projectID empty resolves the story's own
// project. Missing id → ErrNotFound.
func (s *Store) EditStory(projectID, id string, patch StoryPatch) error {
	st, err := s.getStory(projectID, id)
	if err != nil {
		return err
	}
	if !editable(st.Status) {
		return fmt.Errorf("%w: %s is %s (edit allowed only in backlog/failed/in_review)", ErrInvalidState, id, st.Status)
	}
	pid := st.ProjectID
	// Validate a dep replacement BEFORE any write (mirror AddDep): self-dep, existence,
	// and cycle are rejected whole so a bad edit leaves the graph untouched.
	var newDeps []string
	if patch.Deps != nil {
		newDeps = *patch.Deps
		for _, dep := range newDeps {
			if dep == id {
				return fmt.Errorf("%w: %s depends on itself", ErrDepCycle, id)
			}
			ok, err := s.storyExists(dep, pid)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: %s -> %s", ErrDepNotFound, id, dep)
			}
		}
		if err := s.checkNoCycle(id, newDeps, pid); err != nil {
			return err
		}
	}
	// Assemble the field update from the non-nil patch fields.
	sets := []string{}
	setArgs := []any{}
	if patch.Title != nil {
		sets = append(sets, "title=?")
		setArgs = append(setArgs, *patch.Title)
	}
	if patch.Body != nil {
		sets = append(sets, "body=?")
		setArgs = append(setArgs, *patch.Body)
	}
	if patch.Accept != nil {
		sets = append(sets, "accept=?")
		setArgs = append(setArgs, *patch.Accept)
	}
	if patch.Owner != nil {
		sets = append(sets, "owner=?")
		setArgs = append(setArgs, *patch.Owner)
	}
	if patch.ScreenKey != nil {
		sets = append(sets, "screen_key=?")
		setArgs = append(setArgs, *patch.ScreenKey)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(sets) > 0 {
		q := `UPDATE stories SET ` + strings.Join(sets, ", ") + ` WHERE id=? AND project_id=? AND ` + inClause([]Status{StatusBacklog, StatusFailed, StatusInReview})
		a := append(append([]any{}, setArgs...), id, pid)
		res, err := tx.Exec(q, a...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s changed state during edit", ErrInvalidState, id)
		}
	}
	if patch.Deps != nil {
		if _, err := tx.Exec(`DELETE FROM story_deps WHERE story_id=? AND project_id=?`, id, pid); err != nil {
			return err
		}
		for _, dep := range newDeps {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id, project_id) VALUES(?,?,?)`, id, dep, pid); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// SetStoryRun records the run_id that is executing a story. It only writes the
// run_id on a non-terminal story (M5) — recording a run on a done/failed story is
// always a mistake (a stale completion path) and must be a no-op, surfaced as
// ErrIllegalTransition rather than silently stamping a finished story.
func (s *Store) SetStoryRun(id, runID string) error {
	return s.SetStoryRunInProject("", id, runID)
}

// SetStoryRunInProject is SetStoryRun scoped to projectID (empty = legacy unscoped).
func (s *Store) SetStoryRunInProject(projectID, id, runID string) error {
	q := `UPDATE stories SET run_id=? WHERE id=? AND status IN ('backlog','running','in_review')`
	args := []any{runID, id}
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	cur, err := s.statusOfScoped(projectID, id)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s set_run on terminal %s", ErrIllegalTransition, id, cur)
}

// AddDep adds dep story IDs to a story. Silently skips duplicates. It is a
// mutation on an EXISTING graph, so it validates strictly (H6/H7): every dep must
// resolve to an existing story (ErrDepNotFound), a self-dep is rejected, and a dep
// that would close a cycle is rejected (ErrDepCycle). All-or-nothing — no edge is
// written if any dep is invalid.
func (s *Store) AddDep(storyID, projectID string, deps []string) error {
	for _, dep := range deps {
		if dep == storyID {
			return fmt.Errorf("%w: %s depends on itself", ErrDepCycle, storyID)
		}
		ok, err := s.storyExists(dep, projectID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %s -> %s", ErrDepNotFound, storyID, dep)
		}
	}
	if err := s.checkNoCycle(storyID, deps, projectID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, dep := range deps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id, project_id) VALUES(?,?,?)`, storyID, dep, projectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ValidateDeps checks the WHOLE stored dependency graph for two malformations a
// publish can introduce (H6/H7): a dep pointing at an id that is no story (it can
// never reach "done" → silent permanent deadlock), and a cycle (deadlock + wrong
// fire order). It is the publish-level whole-graph check that backstops the
// per-edge checks in CreateStory/AddDep, catching forward references that resolved
// to nothing after the batch completed. Returns ErrDepNotFound or ErrDepCycle.
func (s *Store) ValidateDeps(projectID string) error {
	adj, err := s.loadAllDeps(projectID)
	if err != nil {
		return err
	}
	// Every dep_id must resolve to an existing story.
	for sid, deps := range adj {
		for _, dep := range deps {
			ok, err := s.storyExists(dep, projectID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: %s -> %s", ErrDepNotFound, sid, dep)
			}
		}
	}
	// No cycle: a node from which it can reach itself along dep-edges is on a cycle.
	for sid := range adj {
		for _, dep := range adj[sid] {
			if reaches(adj, dep, sid) {
				return fmt.Errorf("%w: through %s -> %s", ErrDepCycle, sid, dep)
			}
		}
	}
	return nil
}

// storyExists reports whether a story id is present in the store. If projectID
// is non-empty the lookup is scoped to that project; otherwise any project matches.
func (s *Store) storyExists(id, projectID string) (bool, error) {
	var one int
	var err error
	if projectID != "" {
		err = s.db.QueryRow(`SELECT 1 FROM stories WHERE id=? AND project_id=?`, id, projectID).Scan(&one)
	} else {
		err = s.db.QueryRow(`SELECT 1 FROM stories WHERE id=?`, id).Scan(&one)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// checkNoCycle reports whether adding edges from→dep (for each dep in deps) would
// create a dependency cycle in the stored graph. An edge from→dep means "from
// depends on dep". A cycle exists iff `from` is already reachable from some dep by
// following dep-edges — adding from→dep would then close the loop. Forward refs to
// not-yet-stored stories contribute no reachable edges, so they pass here and are
// validated whole-graph at publish time. Returns ErrDepCycle naming the offending
// dep, or nil.
func (s *Store) checkNoCycle(from string, deps []string, projectID string) error {
	if len(deps) == 0 {
		return nil
	}
	// adjacency: story_id -> its dep_ids (the existing graph).
	adj, err := s.loadAllDeps(projectID)
	if err != nil {
		return err
	}
	for _, dep := range deps {
		// Is `from` reachable from `dep` along existing dep-edges? If so, dep already
		// (transitively) depends on from, and from→dep closes a cycle.
		if reaches(adj, dep, from) {
			return fmt.Errorf("%w: %s -> %s closes a cycle", ErrDepCycle, from, dep)
		}
	}
	return nil
}

// loadAllDeps returns the full story_deps adjacency map (story_id -> dep_ids).
// If projectID is non-empty, only deps for that project are loaded.
func (s *Store) loadAllDeps(projectID string) (map[string][]string, error) {
	var rows *sql.Rows
	var err error
	if projectID != "" {
		rows, err = s.db.Query(`SELECT story_id, dep_id FROM story_deps WHERE project_id=?`, projectID)
	} else {
		rows, err = s.db.Query(`SELECT story_id, dep_id FROM story_deps`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	adj := map[string][]string{}
	for rows.Next() {
		var sid, did string
		if err := rows.Scan(&sid, &did); err != nil {
			return nil, err
		}
		adj[sid] = append(adj[sid], did)
	}
	return adj, rows.Err()
}

// reaches reports whether target is reachable from start by following dep-edges in
// adj (iterative DFS; cycle-safe via a visited set).
func reaches(adj map[string][]string, start, target string) bool {
	if start == target {
		return true
	}
	visited := map[string]bool{}
	stack := []string{start}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[n] {
			continue
		}
		visited[n] = true
		for _, next := range adj[n] {
			if next == target {
				return true
			}
			if !visited[next] {
				stack = append(stack, next)
			}
		}
	}
	return false
}

// Ready returns all stories in StatusBacklog whose every dep has StatusDone.
// A story with no deps is ready immediately when its stored status is backlog.
func (s *Store) Ready() ([]Story, error) {
	// Load all backlog stories and check deps in one pass.
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key
		FROM stories WHERE status='backlog' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL, &st.ProjectID, &st.ExternalRef, &st.Kind, &st.ScreenKey); err != nil {
			return nil, err
		}
		candidates = append(candidates, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Story
	for i := range candidates {
		deps, err := s.loadDeps(candidates[i].ID, candidates[i].ProjectID)
		if err != nil {
			return nil, err
		}
		candidates[i].Deps = deps
		ready, err := s.depsDone(deps, candidates[i].ProjectID)
		if err != nil {
			return nil, err
		}
		if ready {
			out = append(out, candidates[i])
		}
	}
	return out, nil
}

// ---- Sprint-batched execution ----------------------------------------------
//
// "Goal mode" fires a whole sprint as ONE run on ONE branch → ONE PR. The store
// surface here is the readiness/claim/completion machinery the orchestrator uses
// to batch a sprint; the per-story machinery above still backs story mode.

// StoriesBySprint returns the sprint's stories ordered TOPOLOGICALLY by their
// intra-sprint deps — a story comes after any of its deps that live in the SAME
// sprint. Deps pointing outside the sprint do not affect intra-sprint order.
// Ties (no ordering constraint between two stories) are broken stably by id, so
// the order is deterministic. Returns an empty slice for a sprint with no stories.
func (s *Store) StoriesBySprint(sprintID string) ([]Story, error) {
	return s.storiesBySprint(sprintID, "")
}

// StoriesBySprintScoped returns a sprint's stories filtered to projectID (empty = all projects).
func (s *Store) StoriesBySprintScoped(sprintID, projectID string) ([]Story, error) {
	return s.storiesBySprint(sprintID, projectID)
}

func (s *Store) storiesBySprint(sprintID, projectID string) ([]Story, error) {
	q := `SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key
		FROM stories WHERE sprint_id=?`
	args := []any{sprintID}
	if projectID != "" {
		q += ` AND project_id=?`
		args = append(args, projectID)
	}
	q += ` ORDER BY id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stories []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL, &st.ProjectID, &st.ExternalRef, &st.Kind, &st.ScreenKey); err != nil {
			return nil, err
		}
		stories = append(stories, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range stories {
		deps, err := s.loadDeps(stories[i].ID, stories[i].ProjectID)
		if err != nil {
			return nil, err
		}
		stories[i].Deps = deps
	}
	return topoSortStories(stories)
}

// topoSortStories returns stories ordered so every story follows the intra-sprint
// deps it has. Ties are broken by id (stable, deterministic). A dependency cycle
// among the stories is a HARD ERROR (ErrDepCycle) — the previous behavior of
// "degrade to id order" silently fired stories before their deps (H7); a cycle is
// a malformed backlog and must surface, not be papered over.
func topoSortStories(stories []Story) ([]Story, error) {
	inSet := make(map[string]bool, len(stories))
	for _, st := range stories {
		inSet[st.ID] = true
	}
	// Remaining unmet intra-sprint deps per story.
	pending := make(map[string]map[string]bool, len(stories))
	for _, st := range stories {
		need := map[string]bool{}
		for _, dep := range st.Deps {
			if inSet[dep] {
				need[dep] = true
			}
		}
		pending[st.ID] = need
	}

	out := make([]Story, 0, len(stories))
	placed := make(map[string]bool, len(stories))
	for len(out) < len(stories) {
		// Pick the lowest-id story whose intra-sprint deps are all placed.
		var next *Story
		for i := range stories {
			st := stories[i]
			if placed[st.ID] {
				continue
			}
			if depsPlaced(pending[st.ID], placed) {
				next = &stories[i]
				break
			}
		}
		if next == nil {
			// No placeable story remains but some are unplaced → a cycle. Name the
			// stuck stories so the malformed backlog is diagnosable.
			var stuck []string
			for i := range stories {
				if !placed[stories[i].ID] {
					stuck = append(stuck, stories[i].ID)
				}
			}
			return nil, fmt.Errorf("%w: among stories %s", ErrDepCycle, strings.Join(stuck, ","))
		}
		out = append(out, *next)
		placed[next.ID] = true
	}
	return out, nil
}

// depsPlaced reports whether every dep in need has already been placed.
func depsPlaced(need, placed map[string]bool) bool {
	for dep := range need {
		if !placed[dep] {
			return false
		}
	}
	return true
}

// ReadySprints returns sprints that can be fired as a single goal-mode run. A
// sprint is ready when: it has ≥1 story, EVERY story is still backlog (none
// started), and every dep pointing OUTSIDE the sprint is done. Intra-sprint deps
// are resolved inside the single run, so they never block sprint readiness.
// Sprints with zero stories are skipped.
func (s *Store) ReadySprints() ([]Sprint, error) {
	return s.ReadySprintsByProject("")
}

// ReadySprintsByProject is ReadySprints scoped to projectID (empty = all
// projects, for admin/back-compat) (audit A1). The scheduler fetches a project's
// ready sprints so each project's work is evaluated under its own settings.
func (s *Store) ReadySprintsByProject(projectID string) ([]Sprint, error) {
	sprints, err := s.listSprints(projectID)
	if err != nil {
		return nil, err
	}
	var out []Sprint
	for _, sp := range sprints {
		ready, err := s.sprintReady(sp.ID, sp.ProjectID)
		if err != nil {
			return nil, err
		}
		if ready {
			out = append(out, sp)
		}
	}
	return out, nil
}

// sprintReady reports whether the sprint at sprintID satisfies the goal-mode
// readiness rule (≥1 story, all backlog, external deps done).
func (s *Store) sprintReady(sprintID, projectID string) (bool, error) {
	stories, err := s.storiesBySprint(sprintID, projectID)
	if err != nil {
		return false, err
	}
	if len(stories) == 0 {
		return false, nil
	}
	inSprint := make(map[string]bool, len(stories))
	for _, st := range stories {
		inSprint[st.ID] = true
	}
	for _, st := range stories {
		if st.Status != StatusBacklog {
			return false, nil // some story already started/finished → not a fresh sprint
		}
		for _, dep := range st.Deps {
			if inSprint[dep] {
				continue // intra-sprint dep — resolved inside the run
			}
			done, err := s.depsDone([]string{dep}, st.ProjectID)
			if err != nil {
				return false, err
			}
			if !done {
				return false, nil // an external dep is not done → sprint blocked
			}
		}
	}
	return true, nil
}

// ClaimSprint atomically transitions ALL of a sprint's stories from backlog →
// running in ONE transaction. It re-verifies every story is still backlog inside
// the transaction; if a concurrent claimer already moved any of them the claim is
// abandoned (ok=false, no error) and nothing is written. On success it returns
// the claimed story IDs in topological order. ok=false with no claimed IDs also
// covers an empty sprint.
func (s *Store) ClaimSprint(sprintID, projectID string) (claimed []string, ok bool, err error) {
	ordered, err := s.storiesBySprint(sprintID, projectID)
	if err != nil {
		return nil, false, err
	}
	if len(ordered) == 0 {
		return nil, false, nil
	}

	// BEGIN IMMEDIATE (via _txlock=immediate DSN) takes the write lock NOW, before
	// the COUNT below — so two concurrent claimers serialize: the winner commits the
	// running flip, the loser then reads the post-commit COUNT, sees the mismatch,
	// and returns ok=false. Without the immediate lock both could pass the COUNT and
	// the loser would hit a hard "database is locked" on its UPDATE instead (H5).
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	// Re-check inside the tx: every story must still be backlog.
	var backlog int
	cntQ := `SELECT COUNT(*) FROM stories WHERE sprint_id=? AND status='backlog'`
	cntArgs := []any{sprintID}
	if projectID != "" {
		cntQ += ` AND project_id=?`
		cntArgs = append(cntArgs, projectID)
	}
	if err := tx.QueryRow(cntQ, cntArgs...).Scan(&backlog); err != nil {
		return nil, false, err
	}
	if backlog != len(ordered) {
		return nil, false, nil // a concurrent claimer moved at least one story
	}

	updQ := `UPDATE stories SET status='running' WHERE sprint_id=? AND status='backlog'`
	updArgs := []any{sprintID}
	if projectID != "" {
		updQ += ` AND project_id=?`
		updArgs = append(updArgs, projectID)
	}
	res, err := tx.Exec(updQ, updArgs...)
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	if int(n) != len(ordered) {
		return nil, false, nil // raced between count and update — abandon, rollback
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}

	ids := make([]string, len(ordered))
	for i, st := range ordered {
		ids[i] = st.ID
	}
	return ids, true, nil
}

// MarkSprintDone advances a sprint's in_review stories to done. ONLY in_review
// stories advance: in the merge-gated lifecycle a sprint's stories sit in_review
// until their shared PR merges, at which point this advances them. The previous
// `IN ('running','in_review')` clause was a correctness bug (B1) — it flipped
// every still-running story to done on another story's merge, fabricating "done"
// on work that was in no merged PR. A running story must never be marked done by a
// sprint-wide merge; it waits for its own in_review parking first.
func (s *Store) MarkSprintDone(sprintID string) error {
	// Capture which stories are about to flip in_review→done so billing can count
	// each as a feature (the bulk UPDATE bypasses transition()). The hook is idempotent.
	var ids []string
	if s.OnStoryDone != nil {
		if rows, err := s.db.Query(`SELECT id FROM stories WHERE sprint_id=? AND status='in_review'`, sprintID); err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
		}
	}
	if _, err := s.db.Exec(`UPDATE stories SET status='done' WHERE sprint_id=? AND status='in_review'`, sprintID); err != nil {
		return err
	}
	for _, id := range ids {
		s.fireStoryDone("", id)
	}
	return nil
}

// MarkSprintFailed advances all of a sprint's non-terminal stories to failed. A
// failure may strike backlog/running/in_review but never re-fails a done/failed
// story (B2 — terminal states are not resurrected).
func (s *Store) MarkSprintFailed(sprintID string) error {
	_, err := s.db.Exec(
		`UPDATE stories SET status='failed' WHERE sprint_id=? AND status IN ('backlog','running','in_review')`,
		sprintID)
	return err
}

// SetSprintRun records runID on the sprint's non-terminal stories so the UI can
// link a goal-mode sprint's stories back to the single run that implemented them.
// Terminal (done/failed) stories are left untouched (M5) — recording a run_id is a
// status-adjacent write and must not silently revive a finished story's linkage.
func (s *Store) SetSprintRun(sprintID, runID string) error {
	_, err := s.db.Exec(
		`UPDATE stories SET run_id=? WHERE sprint_id=? AND status IN ('backlog','running','in_review')`,
		runID, sprintID)
	return err
}

// ---- Merge-gated lifecycle (in_review) --------------------------------------
//
// A run finishing only means its PR is OPEN, not merged. Work therefore moves
// running → in_review (PR recorded) and stays there until the PR is MERGED, at
// which point it advances to done and unblocks dependents. MarkInReview /
// MarkSprintInReview record the PR; InReview lists the work the reconcile loop
// must check; MarkDone / MarkSprintDone (already defined) advance on merge.

// MarkInReview moves a single running story to in_review and records the PR URL
// its run opened. A blank prURL is allowed (the loop will skip it until set).
// Guarded: only a running story may move to in_review, so a stale completion path
// cannot drag a done/failed story back into the reconcile loop (B2).
func (s *Store) MarkInReview(id, prURL string) error {
	return s.MarkInReviewInProject("", id, prURL)
}

// MarkInReviewInProject is MarkInReview scoped to projectID (empty = legacy unscoped).
func (s *Store) MarkInReviewInProject(projectID, id, prURL string) error {
	return s.transitionScoped(projectID, id, StatusInReview, "pr_url=?", prURL)
}

// MarkDone advances a single story to done (used when its in_review PR merges).
// Guarded: only an in_review story may reach done — a still-running story is never
// flipped done, so "done" always means a merged PR (B1/B2).
func (s *Store) MarkDone(id string) error {
	return s.transition(id, StatusDone, "") // transition() fires OnStoryDone for the done target
}

// MarkSprintInReview moves all of a sprint's RUNNING stories to in_review and
// records the shared PR URL on the stories it moves. Only running stories are
// touched (the guarded source), so a story already done/failed in the sprint is
// not pulled back into the reconcile loop.
func (s *Store) MarkSprintInReview(sprintID, prURL string) error {
	_, err := s.db.Exec(`UPDATE stories SET status='in_review', pr_url=? WHERE sprint_id=? AND status='running'`,
		prURL, sprintID)
	return err
}

// InReview returns all stories currently in_review (any sprint or loose). The
// merge-reconcile loop reads these each cycle to check whether their PR merged.
func (s *Store) InReview() ([]Story, error) {
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url, project_id, external_ref, kind, screen_key
		FROM stories WHERE status='in_review' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL, &st.ProjectID, &st.ExternalRef, &st.Kind, &st.ScreenKey); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// loadDeps returns the dep IDs for storyID, sorted. If projectID is non-empty,
// the query is scoped to that project; otherwise all deps for the story are returned
// (back-compat for callers that don't have project context yet).
func (s *Store) loadDeps(storyID, projectID string) ([]string, error) {
	var rows *sql.Rows
	var err error
	if projectID != "" {
		rows, err = s.db.Query(`SELECT dep_id FROM story_deps WHERE story_id=? AND project_id=? ORDER BY dep_id ASC`, storyID, projectID)
	} else {
		rows, err = s.db.Query(`SELECT dep_id FROM story_deps WHERE story_id=? ORDER BY dep_id ASC`, storyID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, err
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

// depsDone reports whether every dep story has StatusDone. If projectID is
// non-empty the lookup is scoped to that project; otherwise the first matching
// story by id is used (back-compat).
func (s *Store) depsDone(deps []string, projectID string) (bool, error) {
	for _, dep := range deps {
		var st Status
		var err error
		if projectID != "" {
			err = s.db.QueryRow(`SELECT status FROM stories WHERE id=? AND project_id=?`, dep, projectID).Scan(&st)
		} else {
			err = s.db.QueryRow(`SELECT status FROM stories WHERE id=?`, dep).Scan(&st)
		}
		if errors.Is(err, sql.ErrNoRows) {
			// D7: a dep pointing at a story that does not exist is corrupt data (deps
			// are validated at publish). Treat it as "not done" so it gates rather
			// than crashes — but LOG it: previously this deadlocked the dependent
			// silently with no signal that the graph was broken.
			log.Printf("tickets: dep %q does not exist — dependent story is gated (corrupt dep graph?)", dep)
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if st != StatusDone {
			return false, nil
		}
	}
	return true, nil
}

// isDuplicateColumn reports whether err is a SQLite "duplicate column name"
// error that signals the column was already added by a prior migration run.
func isDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return indexOf(msg, "duplicate column name") >= 0
}

// ---- JSON helpers for http layer --------------------------------------------

// MarshalDeps encodes a []string to a JSON column (used when embedding deps in
// a single-row response that doesn't use story_deps). Provided for callers that
// prefer a JSON column rather than the join table.
func MarshalDeps(deps []string) string {
	if len(deps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(deps)
	return string(b)
}
