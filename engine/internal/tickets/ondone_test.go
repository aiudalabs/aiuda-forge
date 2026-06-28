package tickets_test

import (
	"path/filepath"
	"testing"

	"forge/internal/tickets"
)

// Regression: the REAL factory path sets a story to done via the HTTP status
// endpoint → UpdateStoryStatus(done) → transition(done), NOT tickets.MarkDone. The
// billing OnStoryDone hook must fire on that path (it lives in transition()), else
// story-mode features would never be billed.
func TestUpdateStoryStatusDoneFiresOnStoryDone(t *testing.T) {
	st, err := tickets.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	var fired []string
	st.OnStoryDone = func(projectID, storyID, runID string) { fired = append(fired, storyID) }

	if err := st.CreateStory(tickets.Story{ID: "S1", Title: "feature", ProjectID: "p1", Status: tickets.StatusBacklog}); err != nil {
		t.Fatal(err)
	}
	// Walk the real path via the generic setter the HTTP endpoint uses.
	for _, s := range []tickets.Status{tickets.StatusRunning, tickets.StatusInReview, tickets.StatusDone} {
		if err := st.UpdateStoryStatus("S1", s); err != nil {
			t.Fatalf("UpdateStoryStatus(S1→%s): %v", s, err)
		}
	}
	if len(fired) != 1 || fired[0] != "S1" {
		t.Fatalf("OnStoryDone must fire exactly once for S1 via UpdateStoryStatus(done), got %v", fired)
	}

	// Re-asserting done is idempotent (no second fire).
	_ = st.UpdateStoryStatus("S1", tickets.StatusDone)
	if len(fired) != 1 {
		t.Fatalf("done→done must not re-fire OnStoryDone, got %v", fired)
	}
}
