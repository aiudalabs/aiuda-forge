package projects_test

import (
	"errors"
	"testing"

	"forge/internal/projects"
)

// TestRetroModeDefaultsAuto: a freshly created project reports retro_mode "auto".
func TestRetroModeDefaultsAuto(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	set, err := st.GetSettings("P")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if set.RetroMode != projects.RetroModeAuto {
		t.Errorf("default retro_mode = %q, want %q", set.RetroMode, projects.RetroModeAuto)
	}
}

// TestRetroModeRoundTrip: PutSettings persists a valid retro_mode; an empty field leaves
// it unchanged and does not disturb the sibling review_mode.
func TestRetroModeRoundTrip(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := st.PutSettings("P", projects.Settings{RetroMode: projects.RetroModeCeremony})
	if err != nil {
		t.Fatalf("put settings: %v", err)
	}
	if out.RetroMode != projects.RetroModeCeremony {
		t.Errorf("returned retro_mode = %q, want %q", out.RetroMode, projects.RetroModeCeremony)
	}
	got, _ := st.GetSettings("P")
	if got.RetroMode != projects.RetroModeCeremony {
		t.Errorf("persisted retro_mode = %q, want %q", got.RetroMode, projects.RetroModeCeremony)
	}
	// A review-only update must NOT reset the stored retro_mode.
	if _, err := st.PutSettings("P", projects.Settings{ReviewMode: projects.ReviewModeCeremony}); err != nil {
		t.Fatalf("put settings (review only): %v", err)
	}
	got, _ = st.GetSettings("P")
	if got.RetroMode != projects.RetroModeCeremony {
		t.Errorf("retro_mode changed by an unrelated update = %q, want ceremony", got.RetroMode)
	}
}

// TestRetroModeInvalidRejected: an unknown retro_mode is ErrInvalid and does not persist.
func TestRetroModeInvalidRejected(t *testing.T) {
	st := openTemp(t)
	if _, err := st.Create(projects.Project{ID: "P", Name: "p", OwnerID: "usr-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := st.PutSettings("P", projects.Settings{RetroMode: "rarely"})
	if !errors.Is(err, projects.ErrInvalid) {
		t.Fatalf("invalid retro_mode err = %v, want ErrInvalid", err)
	}
	got, _ := st.GetSettings("P")
	if got.RetroMode != projects.RetroModeAuto {
		t.Errorf("rejected value must not persist; got %q", got.RetroMode)
	}
}
