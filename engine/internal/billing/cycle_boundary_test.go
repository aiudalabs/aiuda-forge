package billing

import "testing"

// A feature whose work was GENERATED in one cycle but whose PR MERGES in a LATER
// cycle must bill in the MERGE cycle (the merge is the billable event), while its
// token cost stays in the cycle it was actually incurred. This is the cross-cycle
// boundary the dual meters must handle correctly.
func TestFeatureBillsInMergeCycleCostStaysInGenerationCycle(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	_ = st.SetPlan(w.ID, "indie")

	// Cycle 1 — generation: the dev work burns tokens (cost lands here), PR not merged.
	c1, _ := st.ActiveCycle(w.ID)
	if _, _, err := st.AddCost(w.ID, "task-gen", 0.80); err != nil {
		t.Fatal(err)
	}

	// Monthly reset BEFORE the PR merges.
	if _, err := st.RollCycle(w.ID); err != nil {
		t.Fatal(err)
	}
	c2, _ := st.ActiveCycle(w.ID)
	if c2.ID == c1.ID {
		t.Fatal("cycle should have rolled")
	}

	// Cycle 2 — the PR merges now → the billable feature counts in THIS (merge) cycle.
	if _, err := st.CountFeature(w.ID, "S1", "run-x"); err != nil {
		t.Fatal(err)
	}

	// The feature charge is scoped to the MERGE cycle (c2), not the generation cycle.
	var fchCycle string
	if err := st.db.QueryRow(`SELECT cycle_id FROM feature_charges WHERE story_id='S1'`).Scan(&fchCycle); err != nil {
		t.Fatal(err)
	}
	if fchCycle != c2.ID {
		t.Fatalf("feature must bill in the merge cycle %s, got %s", c2.ID, fchCycle)
	}

	// The token cost stays in the GENERATION cycle (c1) — accrued when it was spent.
	var costCycle string
	if err := st.db.QueryRow(`SELECT cycle_id FROM cost_events WHERE task_id='task-gen'`).Scan(&costCycle); err != nil {
		t.Fatal(err)
	}
	if costCycle != c1.ID {
		t.Fatalf("cost must stay in the generation cycle %s, got %s", c1.ID, costCycle)
	}

	// Merge cycle shows the feature; generation cycle shows the cost. (Lifetime margin
	// is correct; per-cycle timing differs — standard accrual.)
	cur, _ := st.ActiveCycle(w.ID)
	if cur.BillableFeatures != 1 || cur.CostUSDIncurred != 0 {
		t.Fatalf("merge cycle should have 1 feature and $0 cost, got %+v", cur)
	}
}
