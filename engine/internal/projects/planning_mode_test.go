package projects_test

import (
	"errors"
	"testing"

	"forge/internal/projects"
)

// TestPlanningModeDefaultsAuto: a freshly created project reports planning_mode
// "auto" (the historical behavior) rather than leaking "".
func TestPlanningModeDefaultsAuto(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	set, err := st.GetSettings("P")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if set.PlanningMode != projects.PlanningModeAuto {
		t.Errorf("default planning_mode = %q, want %q", set.PlanningMode, projects.PlanningModeAuto)
	}
}

// TestPlanningModeRoundTrip: PutSettings persists a valid planning_mode and it
// survives a re-read; an empty field leaves it unchanged.
func TestPlanningModeRoundTrip(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := st.PutSettings("P", projects.Settings{PlanningMode: projects.PlanningModeCeremony})
	if err != nil {
		t.Fatalf("put settings: %v", err)
	}
	if out.PlanningMode != projects.PlanningModeCeremony {
		t.Errorf("returned planning_mode = %q, want %q", out.PlanningMode, projects.PlanningModeCeremony)
	}
	got, err := st.GetSettings("P")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if got.PlanningMode != projects.PlanningModeCeremony {
		t.Errorf("persisted planning_mode = %q, want %q", got.PlanningMode, projects.PlanningModeCeremony)
	}
	// An empty PlanningMode in the patch must NOT reset the stored value.
	if _, err := st.PutSettings("P", projects.Settings{MergeMode: projects.MergeModeAuto}); err != nil {
		t.Fatalf("put settings (merge only): %v", err)
	}
	got, _ = st.GetSettings("P")
	if got.PlanningMode != projects.PlanningModeCeremony {
		t.Errorf("planning_mode changed by an unrelated update = %q, want ceremony", got.PlanningMode)
	}
}

// TestPlanningModeInvalidRejected: an unknown planning_mode is ErrInvalid and does
// not persist.
func TestPlanningModeInvalidRejected(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := st.PutSettings("P", projects.Settings{PlanningMode: "sometimes"})
	if !errors.Is(err, projects.ErrInvalid) {
		t.Fatalf("invalid planning_mode err = %v, want ErrInvalid", err)
	}
	got, _ := st.GetSettings("P")
	if got.PlanningMode != projects.PlanningModeAuto {
		t.Errorf("rejected value must not persist; got %q", got.PlanningMode)
	}
}
