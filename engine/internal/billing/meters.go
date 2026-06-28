package billing

import "database/sql"

// FeatureKind is whether a billed feature consumed an included allowance or overage.
type FeatureKind string

const (
	KindIncluded FeatureKind = "included"
	KindOverage  FeatureKind = "overage"
)

// AddCost records the real token cost of ONE task into the workspace's active cycle
// — meter (a), what we ACTUALLY spend, INCLUDING failed/cancelled tasks (every task
// reports usage). Writes a cost_events row for audit and bumps the cycle aggregate.
// Returns the new cycle total so the spend-cap guard (step 4) can act on it.
func (s *Store) AddCost(workspaceID, taskID string, costUSD float64) (float64, error) {
	c, err := s.ActiveCycle(workspaceID)
	if err != nil {
		return 0, err
	}
	if costUSD <= 0 {
		return c.CostUSDIncurred, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO cost_events(workspace_id, cycle_id, task_id, cost_usd, created_at) VALUES(?,?,?,?,?)`,
		workspaceID, c.ID, taskID, costUSD, s.now()); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE billing_cycles SET cost_usd_incurred = cost_usd_incurred + ? WHERE id=?`, costUSD, c.ID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return c.CostUSDIncurred + costUSD, nil
}

// CountFeature records ONE billable feature for a story that reached DONE — meter
// (b). Idempotent by story_id (a story bills once). It decides included vs overage
// from the plan + current consumption, writes the charge ledger row, and bumps the
// cycle counters (+ the FREE lifetime counter). A story that never reaches DONE
// never calls this, so failed/cancelled/unmerged work bills nothing.
func (s *Store) CountFeature(workspaceID, storyID, runID string) (FeatureKind, error) {
	// Idempotency: a story already charged returns its existing kind, no double count.
	var existing string
	err := s.db.QueryRow(`SELECT kind FROM feature_charges WHERE story_id=?`, storyID).Scan(&existing)
	if err == nil {
		return FeatureKind(existing), nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	w, err := s.GetWorkspace(workspaceID)
	if err != nil {
		return "", err
	}
	plan := PlanFor(w.PlanID)
	c, err := s.ActiveCycle(workspaceID)
	if err != nil {
		return "", err
	}

	kind := KindIncluded
	amount := 0.0
	if !plan.LifetimeIncluded { // paid plans: per-cycle included, then overage
		if c.IncludedConsumed >= plan.IncludedFeatures {
			kind = KindOverage
			amount = plan.OveragePriceUSD
		}
	}
	// FREE (LifetimeIncluded) has no overage — always recorded as included for audit;
	// the entitlement gate (step 2) is what stops a FREE workspace past its 2.

	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO feature_charges(id, workspace_id, cycle_id, story_id, run_id, kind, amount_usd, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		newID("fch"), workspaceID, c.ID, storyID, runID, string(kind), amount, s.now()); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE billing_cycles SET billable_features = billable_features + 1, included_consumed = included_consumed + ?, overage_consumed = overage_consumed + ? WHERE id=?`,
		b2i(kind == KindIncluded), b2i(kind == KindOverage), c.ID); err != nil {
		return "", err
	}
	if plan.LifetimeIncluded {
		if _, err := tx.Exec(`UPDATE workspaces SET lifetime_features = lifetime_features + 1 WHERE id=?`, workspaceID); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return kind, nil
}

// Health is the per-workspace margin view: the gap between what we spend (a) and
// what we bill (b) is the business-health signal. cost_per_feature rising far above
// the overage price, or margin negative, means the customer generates & discards.
type Health struct {
	CostUSDIncurred  float64 `json:"cost_usd_incurred"`
	BillableFeatures int     `json:"billable_features"`
	IncludedConsumed int     `json:"included_consumed"`
	OverageConsumed  int     `json:"overage_consumed"`
	CostPerFeature   float64 `json:"cost_per_feature"`
	MarginUSD        float64 `json:"margin_usd"` // overage revenue − cost incurred (included features are pre-paid by the base price)
}

// CycleHealth computes the margin view for a workspace's active cycle.
func (s *Store) CycleHealth(workspaceID string) (Health, error) {
	w, err := s.GetWorkspace(workspaceID)
	if err != nil {
		return Health{}, err
	}
	c, err := s.ActiveCycle(workspaceID)
	if err != nil {
		return Health{}, err
	}
	plan := PlanFor(w.PlanID)
	h := Health{
		CostUSDIncurred: c.CostUSDIncurred, BillableFeatures: c.BillableFeatures,
		IncludedConsumed: c.IncludedConsumed, OverageConsumed: c.OverageConsumed,
	}
	if c.BillableFeatures > 0 {
		h.CostPerFeature = c.CostUSDIncurred / float64(c.BillableFeatures)
	}
	h.MarginUSD = float64(c.OverageConsumed)*plan.OveragePriceUSD - c.CostUSDIncurred
	return h, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
