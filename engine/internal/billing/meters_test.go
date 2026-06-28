package billing

import "testing"

// Meter (a): real cost accumulates for ALL tasks, including failed ones.
func TestAddCostSumsIncludingFailed(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	// A failed task's cost still counts (it's money we spent).
	if _, err := st.AddCost(w.ID, "task-failed", 0.40); err != nil {
		t.Fatal(err)
	}
	total, err := st.AddCost(w.ID, "task-done", 0.60)
	if err != nil {
		t.Fatal(err)
	}
	if total < 0.999 || total > 1.001 {
		t.Fatalf("cycle cost = %v, want 1.00 (failed + done both counted)", total)
	}
	c, _ := st.ActiveCycle(w.ID)
	if c.CostUSDIncurred < 0.999 || c.CostUSDIncurred > 1.001 {
		t.Fatalf("persisted cost = %v, want 1.00", c.CostUSDIncurred)
	}
}

// Meter (b): a paid plan bills included until the allowance runs out, then overage.
func TestCountFeatureIncludedThenOverage(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	_ = st.SetPlan(w.ID, "indie") // 15 included, overage 3.50
	inc := Plans["indie"].IncludedFeatures

	for i := 0; i < inc; i++ {
		k, err := st.CountFeature(w.ID, story(i), "run-x")
		if err != nil {
			t.Fatal(err)
		}
		if k != KindIncluded {
			t.Fatalf("feature %d should be included, got %s", i, k)
		}
	}
	// One past the included allowance → overage.
	k, err := st.CountFeature(w.ID, story(inc), "run-x")
	if err != nil {
		t.Fatal(err)
	}
	if k != KindOverage {
		t.Fatalf("feature %d should be overage, got %s", inc, k)
	}
	c, _ := st.ActiveCycle(w.ID)
	if c.BillableFeatures != inc+1 || c.IncludedConsumed != inc || c.OverageConsumed != 1 {
		t.Fatalf("counters off: %+v (want billable=%d included=%d overage=1)", c, inc+1, inc)
	}
	h, _ := st.CycleHealth(w.ID)
	if h.MarginUSD != Plans["indie"].OveragePriceUSD { // 1 overage, no cost incurred in this test
		t.Fatalf("margin = %v, want %v (1 overage feature, $0 cost)", h.MarginUSD, Plans["indie"].OveragePriceUSD)
	}
}

// A story bills exactly once, even if DONE is observed twice.
func TestCountFeatureIdempotent(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	_ = st.SetPlan(w.ID, "indie")
	if _, err := st.CountFeature(w.ID, "S1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CountFeature(w.ID, "S1", "run-1"); err != nil { // duplicate DONE
		t.Fatal(err)
	}
	c, _ := st.ActiveCycle(w.ID)
	if c.BillableFeatures != 1 {
		t.Fatalf("a story must bill once, got %d", c.BillableFeatures)
	}
}

// FREE uses a lifetime feature counter (not per-cycle), no overage.
func TestFreeLifetimeCounter(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1") // free by default
	for i := 0; i < 2; i++ {
		k, _ := st.CountFeature(w.ID, story(i), "run-x")
		if k != KindIncluded {
			t.Fatalf("free feature %d should be included, got %s", i, k)
		}
	}
	got, _ := st.GetWorkspace(w.ID)
	if got.LifetimeFeatures != 2 {
		t.Fatalf("free lifetime_features = %d, want 2", got.LifetimeFeatures)
	}
}

func story(i int) string {
	return "S" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}
