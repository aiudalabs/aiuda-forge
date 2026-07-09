package tickets_test

import (
	"errors"
	"testing"

	"forge/internal/tickets"
)

// retroAt reads a sprint's retro_at (test helper).
func retroAt(t *testing.T, st *tickets.Store, sprintID string) int64 {
	t.Helper()
	sps, err := st.ListSprintsByProject("p1")
	if err != nil {
		t.Fatalf("list sprints: %v", err)
	}
	for _, sp := range sps {
		if sp.ID == sprintID {
			return sp.RetroAt
		}
	}
	t.Fatalf("sprint %s not found", sprintID)
	return 0
}

// TestReviewedSprintsAwaitingRetro returns only sprints that are reviewed (reviewed_at
// != 0) and not yet retro'd (retro_at == 0) — the order review→retro is enforced here.
func TestReviewedSprintsAwaitingRetro(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1") // reviewed, not retro'd → awaiting retro
	if err := st.SetSprintReviewed("SP1", "p1"); err != nil {
		t.Fatalf("review SP1: %v", err)
	}
	mkSprint(t, st, "SP2") // reviewed AND retro'd → excluded
	if err := st.SetSprintReviewed("SP2", "p1"); err != nil {
		t.Fatalf("review SP2: %v", err)
	}
	if err := st.SetSprintRetroed("SP2", "p1"); err != nil {
		t.Fatalf("retro SP2: %v", err)
	}
	mkSprint(t, st, "SP3") // never reviewed → excluded (retro waits for review)

	got, err := st.ReviewedSprintsAwaitingRetro("p1")
	if err != nil {
		t.Fatalf("ReviewedSprintsAwaitingRetro: %v", err)
	}
	if len(got) != 1 || got[0].ID != "SP1" {
		ids := make([]string, len(got))
		for i, s := range got {
			ids[i] = s.ID
		}
		t.Fatalf("want [SP1] awaiting retro, got %v", ids)
	}
}

// TestSetSprintRetroRunAndRetroed round-trips the two retro columns and rejects an
// unknown sprint (ErrNotFound).
func TestSetSprintRetroRunAndRetroed(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")

	if err := st.SetSprintRetroRun("SP1", "p1", "run-r"); err != nil {
		t.Fatalf("SetSprintRetroRun: %v", err)
	}
	if err := st.SetSprintRetroed("SP1", "p1"); err != nil {
		t.Fatalf("SetSprintRetroed: %v", err)
	}
	sps, _ := st.ListSprintsByProject("p1")
	if len(sps) != 1 || sps[0].RetroRunID != "run-r" || sps[0].RetroAt == 0 {
		t.Fatalf("retro columns not persisted: %+v", sps)
	}
	if got := retroAt(t, st, "SP1"); got == 0 {
		t.Fatal("retro_at should be stamped")
	}

	if err := st.SetSprintRetroRun("nope", "p1", "r"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("SetSprintRetroRun(unknown) = %v, want ErrNotFound", err)
	}
	if err := st.SetSprintRetroed("nope", "p1"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("SetSprintRetroed(unknown) = %v, want ErrNotFound", err)
	}
}
