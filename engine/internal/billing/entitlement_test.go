package billing

import "testing"

func entWorkspace(t *testing.T, plan string) (*Store, string) {
	t.Helper()
	st := openTest(t)
	w, _ := st.WorkspaceForOwner("usr-1")
	if plan != "" {
		_ = st.SetPlan(w.ID, plan)
	}
	return st, w.ID
}

// Case 1: within included → allowed/included.
func TestEntitlementWithinIncluded(t *testing.T) {
	st, ws := entWorkspace(t, "indie")
	d, err := st.Entitlement(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed || d.Mode != ModeIncluded {
		t.Fatalf("fresh indie should be allowed/included, got %+v", d)
	}
}

// Case 2: included exhausted, overage available → allowed/overage.
func TestEntitlementOverage(t *testing.T) {
	st, ws := entWorkspace(t, "indie")
	for i := 0; i < Plans["indie"].IncludedFeatures; i++ {
		_, _ = st.CountFeature(ws, story(i), "run")
	}
	d, _ := st.Entitlement(ws)
	if !d.Allowed || d.Mode != ModeOverage {
		t.Fatalf("indie past included should be allowed/overage, got %+v", d)
	}
}

// Case 3a: overage cap reached → denied.
func TestEntitlementOverageCapDenied(t *testing.T) {
	st, ws := entWorkspace(t, "indie")
	_ = st.SetOverageCap(ws, 3.00) // below one overage feature (3.50) → no overage allowed
	for i := 0; i < Plans["indie"].IncludedFeatures; i++ {
		_, _ = st.CountFeature(ws, story(i), "run")
	}
	d, _ := st.Entitlement(ws)
	if d.Allowed || d.Mode != ModeDenied {
		t.Fatalf("overage cap should deny, got %+v", d)
	}
}

// Case 3b: our spend-cap pause → denied.
func TestEntitlementPausedDenied(t *testing.T) {
	st, ws := entWorkspace(t, "indie")
	_ = st.SetPaused(ws, true, "spend cap reached")
	d, _ := st.Entitlement(ws)
	if d.Allowed || d.Mode != ModeDenied {
		t.Fatalf("paused workspace should deny, got %+v", d)
	}
}

// Case 4: FREE exhausted (2 lifetime) → denied; no overage.
func TestEntitlementFreeExhausted(t *testing.T) {
	st, ws := entWorkspace(t, "") // free
	// First 2 allowed.
	for i := 0; i < 2; i++ {
		d, _ := st.Entitlement(ws)
		if !d.Allowed || d.Mode != ModeIncluded {
			t.Fatalf("free feature %d should be allowed/included, got %+v", i, d)
		}
		_, _ = st.CountFeature(ws, story(i), "run")
	}
	// Third denied.
	d, _ := st.Entitlement(ws)
	if d.Allowed || d.Mode != ModeDenied {
		t.Fatalf("free tier should be exhausted after 2, got %+v", d)
	}
}
