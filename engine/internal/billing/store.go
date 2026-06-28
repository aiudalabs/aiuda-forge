package billing

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Workspace is the billable tenant — one per owner (single-owner today; the
// owner_user_id is the hook for multi-member later). It carries the plan, our hard
// spend cap, the FREE lifetime feature counter, and a paused flag the spend cap sets.
type Workspace struct {
	ID               string  `json:"id"`
	OwnerUserID      string  `json:"owner_user_id"`
	PlanID           string  `json:"plan_id"`
	SpendCapUSD      float64 `json:"spend_cap_usd"`
	LifetimeFeatures int     `json:"lifetime_features"`
	Paused           bool    `json:"paused"`
	PausedReason     string  `json:"paused_reason"`
	CreatedAt        int64   `json:"created_at"`
}

// Cycle is one billing cycle for a workspace. It holds the two dual meters:
// CostUSDIncurred (what we really spend, incl. failed tasks) and BillableFeatures
// (tasks that reached DONE), plus how the included/overage allowance was consumed.
type Cycle struct {
	ID               string  `json:"id"`
	WorkspaceID      string  `json:"workspace_id"`
	StartedAt        int64   `json:"started_at"`
	EndsAt           int64   `json:"ends_at"`
	Status           string  `json:"status"` // active | closed
	CostUSDIncurred  float64 `json:"cost_usd_incurred"`
	BillableFeatures int     `json:"billable_features"`
	IncludedConsumed int     `json:"included_consumed"`
	OverageConsumed  int     `json:"overage_consumed"`
}

// Store persists workspaces, billing cycles, the per-feature charge ledger, and the
// per-task real-cost ledger. Same DSN/schema pattern as internal/auth and tickets.
type Store struct {
	db  *sql.DB
	Now func() time.Time // injectable for tests
}

const schema = `
CREATE TABLE IF NOT EXISTS workspaces (
  id                TEXT PRIMARY KEY,
  owner_user_id     TEXT NOT NULL UNIQUE,
  plan_id           TEXT NOT NULL DEFAULT 'free',
  spend_cap_usd     REAL NOT NULL DEFAULT 0,
  lifetime_features INTEGER NOT NULL DEFAULT 0,
  paused            INTEGER NOT NULL DEFAULT 0,
  paused_reason     TEXT NOT NULL DEFAULT '',
  created_at        INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS billing_cycles (
  id                TEXT PRIMARY KEY,
  workspace_id      TEXT NOT NULL,
  started_at        INTEGER NOT NULL,
  ends_at           INTEGER NOT NULL,
  status            TEXT NOT NULL DEFAULT 'active',
  cost_usd_incurred REAL NOT NULL DEFAULT 0,
  billable_features INTEGER NOT NULL DEFAULT 0,
  included_consumed INTEGER NOT NULL DEFAULT 0,
  overage_consumed  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_cycles_ws ON billing_cycles(workspace_id, status);
CREATE TABLE IF NOT EXISTS feature_charges (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  cycle_id     TEXT NOT NULL,
  story_id     TEXT NOT NULL UNIQUE,
  run_id       TEXT NOT NULL DEFAULT '',
  kind         TEXT NOT NULL,
  amount_usd   REAL NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS cost_events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id TEXT NOT NULL,
  cycle_id     TEXT NOT NULL,
  task_id      TEXT NOT NULL,
  cost_usd     REAL NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_cost_events_ws ON cost_events(workspace_id, cycle_id);
`

// Open opens (creating if needed) the billing sqlite database at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("billing schema: %w", err)
	}
	return &Store{db: db, Now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) now() int64   { return s.Now().UnixMilli() }

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// WorkspaceForOwner returns the owner's workspace, creating it (plan=free) the first
// time. One workspace per owner — the billing tenant. ownerID "" (service/system)
// returns a sentinel system workspace so internal runs never trip billing.
func (s *Store) WorkspaceForOwner(ownerID string) (Workspace, error) {
	var w Workspace
	err := s.db.QueryRow(`SELECT id, owner_user_id, plan_id, spend_cap_usd, lifetime_features, paused, paused_reason, created_at
		FROM workspaces WHERE owner_user_id=?`, ownerID).
		Scan(&w.ID, &w.OwnerUserID, &w.PlanID, &w.SpendCapUSD, &w.LifetimeFeatures, &w.Paused, &w.PausedReason, &w.CreatedAt)
	if err == nil {
		return w, nil
	}
	if err != sql.ErrNoRows {
		return Workspace{}, err
	}
	plan := Plans[DefaultPlanID]
	w = Workspace{
		ID: newID("ws"), OwnerUserID: ownerID, PlanID: plan.ID,
		SpendCapUSD: plan.DefaultSpendCapUSD, CreatedAt: s.now(),
	}
	_, err = s.db.Exec(`INSERT INTO workspaces(id, owner_user_id, plan_id, spend_cap_usd, lifetime_features, paused, paused_reason, created_at)
		VALUES(?,?,?,?,?,?,?,?)`, w.ID, w.OwnerUserID, w.PlanID, w.SpendCapUSD, 0, 0, "", w.CreatedAt)
	if err != nil {
		// A concurrent create (UNIQUE on owner) — re-read the winner.
		if w2, e2 := s.getWorkspaceByOwner(ownerID); e2 == nil {
			return w2, nil
		}
		return Workspace{}, err
	}
	return w, nil
}

func (s *Store) getWorkspaceByOwner(ownerID string) (Workspace, error) {
	var w Workspace
	err := s.db.QueryRow(`SELECT id, owner_user_id, plan_id, spend_cap_usd, lifetime_features, paused, paused_reason, created_at
		FROM workspaces WHERE owner_user_id=?`, ownerID).
		Scan(&w.ID, &w.OwnerUserID, &w.PlanID, &w.SpendCapUSD, &w.LifetimeFeatures, &w.Paused, &w.PausedReason, &w.CreatedAt)
	return w, err
}

// GetWorkspace loads a workspace by id.
func (s *Store) GetWorkspace(id string) (Workspace, error) {
	var w Workspace
	err := s.db.QueryRow(`SELECT id, owner_user_id, plan_id, spend_cap_usd, lifetime_features, paused, paused_reason, created_at
		FROM workspaces WHERE id=?`, id).
		Scan(&w.ID, &w.OwnerUserID, &w.PlanID, &w.SpendCapUSD, &w.LifetimeFeatures, &w.Paused, &w.PausedReason, &w.CreatedAt)
	if err == sql.ErrNoRows {
		return Workspace{}, fmt.Errorf("workspace not found: %s", id)
	}
	return w, err
}

// SetPlan changes a workspace's plan and resets its spend cap to the plan default.
func (s *Store) SetPlan(workspaceID, planID string) error {
	p := PlanFor(planID)
	_, err := s.db.Exec(`UPDATE workspaces SET plan_id=?, spend_cap_usd=? WHERE id=?`, p.ID, p.DefaultSpendCapUSD, workspaceID)
	return err
}

// ActiveCycle returns the workspace's open cycle, opening one (monthly) the first
// time or after the previous one closed.
func (s *Store) ActiveCycle(workspaceID string) (Cycle, error) {
	var c Cycle
	err := s.db.QueryRow(`SELECT id, workspace_id, started_at, ends_at, status, cost_usd_incurred, billable_features, included_consumed, overage_consumed
		FROM billing_cycles WHERE workspace_id=? AND status='active' ORDER BY started_at DESC LIMIT 1`, workspaceID).
		Scan(&c.ID, &c.WorkspaceID, &c.StartedAt, &c.EndsAt, &c.Status, &c.CostUSDIncurred, &c.BillableFeatures, &c.IncludedConsumed, &c.OverageConsumed)
	if err == nil {
		return c, nil
	}
	if err != sql.ErrNoRows {
		return Cycle{}, err
	}
	return s.openCycle(workspaceID)
}

func (s *Store) openCycle(workspaceID string) (Cycle, error) {
	now := s.Now()
	c := Cycle{
		ID: newID("cyc"), WorkspaceID: workspaceID,
		StartedAt: now.UnixMilli(), EndsAt: now.AddDate(0, 1, 0).UnixMilli(), Status: "active",
	}
	_, err := s.db.Exec(`INSERT INTO billing_cycles(id, workspace_id, started_at, ends_at, status) VALUES(?,?,?,?,?)`,
		c.ID, c.WorkspaceID, c.StartedAt, c.EndsAt, c.Status)
	return c, err
}

// RollCycle closes the active cycle and opens a fresh one (monthly reset). Returns
// the new cycle. The closed cycle's meters are retained for history/invoicing.
func (s *Store) RollCycle(workspaceID string) (Cycle, error) {
	if _, err := s.db.Exec(`UPDATE billing_cycles SET status='closed' WHERE workspace_id=? AND status='active'`, workspaceID); err != nil {
		return Cycle{}, err
	}
	return s.openCycle(workspaceID)
}
