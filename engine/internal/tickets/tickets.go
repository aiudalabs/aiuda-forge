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
	"strings"

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
)

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

// Sprint is a time-box that Stories can be assigned to.
type Sprint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Goal string `json:"goal"`
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
}

const schema = `
CREATE TABLE IF NOT EXISTS epics (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sprints (
  id   TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  goal TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS stories (
  id        TEXT PRIMARY KEY,
  epic_id   TEXT NOT NULL DEFAULT '',
  sprint_id TEXT NOT NULL DEFAULT '',
  title     TEXT NOT NULL DEFAULT '',
  body      TEXT NOT NULL DEFAULT '',
  accept    TEXT NOT NULL DEFAULT '',
  owner     TEXT NOT NULL DEFAULT '',
  status    TEXT NOT NULL DEFAULT 'backlog',
  run_id    TEXT NOT NULL DEFAULT '',
  repo      TEXT NOT NULL DEFAULT '',
  pr_url    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS story_deps (
  story_id TEXT NOT NULL,
  dep_id   TEXT NOT NULL,
  PRIMARY KEY (story_id, dep_id)
);
CREATE INDEX IF NOT EXISTS idx_story_deps_story ON story_deps(story_id);
CREATE INDEX IF NOT EXISTS idx_story_deps_dep   ON story_deps(dep_id);
`

// migrationAddRepo is an upgrade guard that adds the repo column to existing
// databases that predate its introduction. SQLite's "duplicate column" error
// (code 1) is silently swallowed so an already-migrated DB is a no-op.
const migrationAddRepo = `ALTER TABLE stories ADD COLUMN repo TEXT NOT NULL DEFAULT ''`

// migrationAddPRURL adds the pr_url column to databases predating the merge-gated
// lifecycle. Same swallow-on-duplicate contract as migrationAddRepo.
const migrationAddPRURL = `ALTER TABLE stories ADD COLUMN pr_url TEXT NOT NULL DEFAULT ''`

// Store is the ticket store backed by a sqlite database.
type Store struct {
	db *sql.DB
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
	return &Store{db: db}, nil
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

// ---- Sprints ----------------------------------------------------------------

// CreateSprint inserts a Sprint.
func (s *Store) CreateSprint(sp Sprint) error {
	if sp.ID == "" {
		return errors.New("sprint id is required")
	}
	_, err := s.db.Exec(`INSERT INTO sprints(id, name, goal) VALUES(?,?,?)`,
		sp.ID, sp.Name, sp.Goal)
	return err
}

// ListSprints returns all sprints ordered by id.
func (s *Store) ListSprints() ([]Sprint, error) {
	rows, err := s.db.Query(`SELECT id, name, goal FROM sprints ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sprint
	for rows.Next() {
		var sp Sprint
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.Goal); err != nil {
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
	if err := s.checkNoCycle(st.ID, st.Deps); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO stories(id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		st.ID, st.EpicID, st.SprintID, st.Title, st.Body, st.Accept, st.Owner, string(st.Status), st.RunID, st.Repo, st.PRURL)
	if err != nil {
		return err
	}
	for _, dep := range st.Deps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id) VALUES(?,?)`, st.ID, dep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetStory loads a Story by id, including its deps.
func (s *Store) GetStory(id string) (Story, error) {
	var st Story
	err := s.db.QueryRow(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url
		FROM stories WHERE id=?`, id).
		Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL)
	if errors.Is(err, sql.ErrNoRows) {
		return Story{}, ErrNotFound
	}
	if err != nil {
		return Story{}, err
	}
	deps, err := s.loadDeps(id)
	if err != nil {
		return Story{}, err
	}
	st.Deps = deps
	return st, nil
}

// ListStories returns all stories with their deps.
func (s *Store) ListStories() ([]Story, error) {
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url
		FROM stories ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		deps, err := s.loadDeps(out[i].ID)
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
	q := fmt.Sprintf(`UPDATE stories SET %s WHERE id=? AND %s`, set, inClause(sources))
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return nil
	}
	// 0 rows: either the id is missing or its current state is an illegal source.
	var cur Status
	err = s.db.QueryRow(`SELECT status FROM stories WHERE id=?`, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
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
	cur, err := s.statusOf(id)
	if err != nil {
		return err
	}
	if cur == status {
		return nil // idempotent: already in the requested state
	}
	if status == StatusBacklog {
		return s.MarkBacklog(id)
	}
	return s.transition(id, status, "")
}

// statusOf returns a story's current stored status, or ErrNotFound.
func (s *Store) statusOf(id string) (Status, error) {
	var cur Status
	err := s.db.QueryRow(`SELECT status FROM stories WHERE id=?`, id).Scan(&cur)
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
	res, err := s.db.Exec(`UPDATE stories SET status='running' WHERE id=? AND status='backlog'`, id)
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
	res, err := s.db.Exec(`UPDATE stories SET status='backlog', run_id='' WHERE id=? AND status='running' AND run_id=''`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var cur Status
	err = s.db.QueryRow(`SELECT status FROM stories WHERE id=?`, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
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

// SetStoryRun records the run_id that is executing a story. It only writes the
// run_id on a non-terminal story (M5) — recording a run on a done/failed story is
// always a mistake (a stale completion path) and must be a no-op, surfaced as
// ErrIllegalTransition rather than silently stamping a finished story.
func (s *Store) SetStoryRun(id, runID string) error {
	res, err := s.db.Exec(
		`UPDATE stories SET run_id=? WHERE id=? AND status IN ('backlog','running','in_review')`, runID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var cur Status
	err = s.db.QueryRow(`SELECT status FROM stories WHERE id=?`, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
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
func (s *Store) AddDep(storyID string, deps []string) error {
	for _, dep := range deps {
		if dep == storyID {
			return fmt.Errorf("%w: %s depends on itself", ErrDepCycle, storyID)
		}
		ok, err := s.storyExists(dep)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %s -> %s", ErrDepNotFound, storyID, dep)
		}
	}
	if err := s.checkNoCycle(storyID, deps); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, dep := range deps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO story_deps(story_id, dep_id) VALUES(?,?)`, storyID, dep); err != nil {
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
func (s *Store) ValidateDeps() error {
	adj, err := s.loadAllDeps()
	if err != nil {
		return err
	}
	// Every dep_id must resolve to an existing story.
	for sid, deps := range adj {
		for _, dep := range deps {
			ok, err := s.storyExists(dep)
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

// storyExists reports whether a story id is present in the store.
func (s *Store) storyExists(id string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM stories WHERE id=?`, id).Scan(&one)
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
func (s *Store) checkNoCycle(from string, deps []string) error {
	if len(deps) == 0 {
		return nil
	}
	// adjacency: story_id -> its dep_ids (the existing graph).
	adj, err := s.loadAllDeps()
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
func (s *Store) loadAllDeps() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT story_id, dep_id FROM story_deps`)
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
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url
		FROM stories WHERE status='backlog' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL); err != nil {
			return nil, err
		}
		candidates = append(candidates, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Story
	for i := range candidates {
		deps, err := s.loadDeps(candidates[i].ID)
		if err != nil {
			return nil, err
		}
		candidates[i].Deps = deps
		ready, err := s.depsDone(deps)
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
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url
		FROM stories WHERE sprint_id=? ORDER BY id ASC`, sprintID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stories []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL); err != nil {
			return nil, err
		}
		stories = append(stories, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range stories {
		deps, err := s.loadDeps(stories[i].ID)
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
	sprints, err := s.ListSprints()
	if err != nil {
		return nil, err
	}
	var out []Sprint
	for _, sp := range sprints {
		ready, err := s.sprintReady(sp.ID)
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
func (s *Store) sprintReady(sprintID string) (bool, error) {
	stories, err := s.StoriesBySprint(sprintID)
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
			done, err := s.depsDone([]string{dep})
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
func (s *Store) ClaimSprint(sprintID string) (claimed []string, ok bool, err error) {
	ordered, err := s.StoriesBySprint(sprintID)
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
	if err := tx.QueryRow(`SELECT COUNT(*) FROM stories WHERE sprint_id=? AND status='backlog'`, sprintID).
		Scan(&backlog); err != nil {
		return nil, false, err
	}
	if backlog != len(ordered) {
		return nil, false, nil // a concurrent claimer moved at least one story
	}

	res, err := tx.Exec(`UPDATE stories SET status='running' WHERE sprint_id=? AND status='backlog'`, sprintID)
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
	_, err := s.db.Exec(`UPDATE stories SET status='done' WHERE sprint_id=? AND status='in_review'`, sprintID)
	return err
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
	return s.transition(id, StatusInReview, "pr_url=?", prURL)
}

// MarkDone advances a single story to done (used when its in_review PR merges).
// Guarded: only an in_review story may reach done — a still-running story is never
// flipped done, so "done" always means a merged PR (B1/B2).
func (s *Store) MarkDone(id string) error {
	return s.transition(id, StatusDone, "")
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
	rows, err := s.db.Query(`SELECT id, epic_id, sprint_id, title, body, accept, owner, status, run_id, repo, pr_url
		FROM stories WHERE status='in_review' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Story
	for rows.Next() {
		var st Story
		if err := rows.Scan(&st.ID, &st.EpicID, &st.SprintID, &st.Title, &st.Body, &st.Accept, &st.Owner, &st.Status, &st.RunID, &st.Repo, &st.PRURL); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// loadDeps returns the dep IDs for storyID, sorted.
func (s *Store) loadDeps(storyID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT dep_id FROM story_deps WHERE story_id=? ORDER BY dep_id ASC`, storyID)
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

// depsDone reports whether every dep story has StatusDone.
func (s *Store) depsDone(deps []string) (bool, error) {
	for _, dep := range deps {
		var st Status
		err := s.db.QueryRow(`SELECT status FROM stories WHERE id=?`, dep).Scan(&st)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil // dep doesn't exist yet → not done
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
