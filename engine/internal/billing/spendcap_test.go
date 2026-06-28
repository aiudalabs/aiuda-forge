package billing

import "testing"

// Step 4: when real token cost crosses OUR hard spend cap, the workspace is paused
// and the entitlement gate then denies it — our protection against a runaway/abuse,
// independent of what the customer is billed.
func TestSpendCapPausesAndDenies(t *testing.T) {
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	_ = st.SetPlan(w.ID, "indie")
	cap := Plans["indie"].DefaultSpendCapUSD

	// Below the cap: no trip.
	_, tripped, err := st.AddCost(w.ID, "t1", cap-5)
	if err != nil {
		t.Fatal(err)
	}
	if tripped {
		t.Fatalf("must not trip below the cap")
	}
	got, _ := st.GetWorkspace(w.ID)
	if got.Paused {
		t.Fatalf("must not be paused below the cap")
	}

	// Crossing the cap: trips + pauses.
	_, tripped, _ = st.AddCost(w.ID, "t2", 10)
	if !tripped {
		t.Fatalf("crossing the cap must trip")
	}
	got, _ = st.GetWorkspace(w.ID)
	if !got.Paused {
		t.Fatalf("workspace must be paused after the spend cap")
	}

	// Entitlement now denies (no more runs until an admin intervenes).
	d, _ := st.Entitlement(w.ID)
	if d.Allowed || d.Mode != ModeDenied {
		t.Fatalf("capped/paused workspace must be denied, got %+v", d)
	}

	// A second cost over the cap does NOT re-trip (already paused).
	_, tripped, _ = st.AddCost(w.ID, "t3", 5)
	if tripped {
		t.Fatalf("must not re-trip an already-paused workspace")
	}
}
