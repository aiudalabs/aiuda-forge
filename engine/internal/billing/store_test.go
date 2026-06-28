package billing

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "billing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestWorkspaceForOwnerIsGetOrCreate(t *testing.T) {
	st := openTest(t)
	w1, err := st.WorkspaceForOwner("usr-1")
	if err != nil {
		t.Fatal(err)
	}
	if w1.PlanID != "free" {
		t.Errorf("new workspace plan = %q, want free", w1.PlanID)
	}
	if w1.SpendCapUSD != Plans["free"].DefaultSpendCapUSD {
		t.Errorf("spend cap = %v, want plan default %v", w1.SpendCapUSD, Plans["free"].DefaultSpendCapUSD)
	}
	// Same owner → same workspace (no duplicate).
	w2, err := st.WorkspaceForOwner("usr-1")
	if err != nil {
		t.Fatal(err)
	}
	if w2.ID != w1.ID {
		t.Fatalf("get-or-create returned a different workspace: %s vs %s", w2.ID, w1.ID)
	}
	// Different owner → different workspace.
	w3, _ := st.WorkspaceForOwner("usr-2")
	if w3.ID == w1.ID {
		t.Fatalf("different owners must get different workspaces")
	}
}

func TestSetPlanUpdatesCapAndPlan(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	if err := st.SetPlan(w.ID, "team"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetWorkspace(w.ID)
	if got.PlanID != "team" {
		t.Errorf("plan = %q, want team", got.PlanID)
	}
	if got.SpendCapUSD != Plans["team"].DefaultSpendCapUSD {
		t.Errorf("spend cap = %v, want team default %v", got.SpendCapUSD, Plans["team"].DefaultSpendCapUSD)
	}
}

func TestActiveCycleIsGetOrCreateAndRolls(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	c1, err := st.ActiveCycle(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c1.Status != "active" || c1.EndsAt <= c1.StartedAt {
		t.Fatalf("bad cycle: %+v", c1)
	}
	c2, _ := st.ActiveCycle(w.ID)
	if c2.ID != c1.ID {
		t.Fatalf("ActiveCycle must be idempotent, got %s vs %s", c2.ID, c1.ID)
	}
	// Roll → a new active cycle, the old one closed.
	c3, err := st.RollCycle(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c3.ID == c1.ID {
		t.Fatalf("RollCycle must open a NEW cycle")
	}
	cur, _ := st.ActiveCycle(w.ID)
	if cur.ID != c3.ID {
		t.Fatalf("active cycle after roll should be the new one")
	}
}

func TestPlanForFallsBackToDefault(t *testing.T) {
	if PlanFor("nope").ID != DefaultPlanID {
		t.Errorf("unknown plan should fall back to %q", DefaultPlanID)
	}
	if PlanFor("indie").IncludedFeatures != 15 {
		t.Errorf("indie included = %d, want 15", PlanFor("indie").IncludedFeatures)
	}
}
