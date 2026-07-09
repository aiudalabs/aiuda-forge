package projects_test

import (
	"errors"
	"testing"

	"forge/internal/projects"
)

// TestReviewModeDefaultsAuto: a freshly created project reports review_mode "auto"
// (the historical behavior) rather than leaking "".
func TestReviewModeDefaultsAuto(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	set, err := st.GetSettings("P")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if set.ReviewMode != projects.ReviewModeAuto {
		t.Errorf("default review_mode = %q, want %q", set.ReviewMode, projects.ReviewModeAuto)
	}
}

// TestReviewModeRoundTrip: PutSettings persists a valid review_mode; an empty field
// leaves it unchanged and does not disturb the sibling planning_mode.
func TestReviewModeRoundTrip(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := st.PutSettings("P", projects.Settings{ReviewMode: projects.ReviewModeCeremony})
	if err != nil {
		t.Fatalf("put settings: %v", err)
	}
	if out.ReviewMode != projects.ReviewModeCeremony {
		t.Errorf("returned review_mode = %q, want %q", out.ReviewMode, projects.ReviewModeCeremony)
	}
	got, err := st.GetSettings("P")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if got.ReviewMode != projects.ReviewModeCeremony {
		t.Errorf("persisted review_mode = %q, want %q", got.ReviewMode, projects.ReviewModeCeremony)
	}
	// An empty ReviewMode in the patch must NOT reset the stored value.
	if _, err := st.PutSettings("P", projects.Settings{MergeMode: projects.MergeModeAuto}); err != nil {
		t.Fatalf("put settings (merge only): %v", err)
	}
	got, _ = st.GetSettings("P")
	if got.ReviewMode != projects.ReviewModeCeremony {
		t.Errorf("review_mode changed by an unrelated update = %q, want ceremony", got.ReviewMode)
	}
}

// TestReviewModeInvalidRejected: an unknown review_mode is ErrInvalid and does not
// persist.
func TestReviewModeInvalidRejected(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := st.PutSettings("P", projects.Settings{ReviewMode: "occasionally"})
	if !errors.Is(err, projects.ErrInvalid) {
		t.Fatalf("invalid review_mode err = %v, want ErrInvalid", err)
	}
	got, _ := st.GetSettings("P")
	if got.ReviewMode != projects.ReviewModeAuto {
		t.Errorf("rejected value must not persist; got %q", got.ReviewMode)
	}
}
