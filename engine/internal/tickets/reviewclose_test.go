package tickets_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"forge/internal/tickets"
	"forge/internal/workflow"
)

// doneStory creates a story already in the done state (a shipped increment's unit) in
// sprint `sprint`, project p1.
func doneStory(t *testing.T, st *tickets.Store, id, sprint string) {
	t.Helper()
	if err := st.CreateStory(tickets.Story{ID: id, SprintID: sprint, ProjectID: "p1", Title: id, Status: tickets.StatusDone}); err != nil {
		t.Fatalf("create done story %s: %v", id, err)
	}
}

func reviewedAt(t *testing.T, st *tickets.Store, sprintID string) int64 {
	t.Helper()
	sps, err := st.ListSprintsByProject("p1")
	if err != nil {
		t.Fatalf("list sprints: %v", err)
	}
	for _, sp := range sps {
		if sp.ID == sprintID {
			return sp.ReviewedAt
		}
	}
	t.Fatalf("sprint %s not found", sprintID)
	return 0
}

// TestDoneSprintsAwaitingReview returns only sprints whose EVERY story is done and
// which are not yet reviewed — a mixed/backlog sprint, an empty sprint, and an
// already-reviewed sprint are all excluded.
func TestDoneSprintsAwaitingReview(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1") // all done → awaiting review
	doneStory(t, st, "A", "SP1")
	doneStory(t, st, "B", "SP1")
	mkSprint(t, st, "SP2") // one story still backlog → NOT a complete increment
	doneStory(t, st, "C", "SP2")
	mkStory(t, st, "D", "SP2")
	mkSprint(t, st, "SP3") // no stories → not offered
	mkSprint(t, st, "SP4") // all done but already reviewed
	doneStory(t, st, "E", "SP4")
	if err := st.SetSprintReviewed("SP4", "p1"); err != nil {
		t.Fatalf("mark SP4 reviewed: %v", err)
	}

	got, err := st.DoneSprintsAwaitingReview("p1")
	if err != nil {
		t.Fatalf("DoneSprintsAwaitingReview: %v", err)
	}
	if len(got) != 1 || got[0].ID != "SP1" {
		ids := make([]string, len(got))
		for i, s := range got {
			ids[i] = s.ID
		}
		t.Fatalf("want [SP1] awaiting review, got %v", ids)
	}
}

// TestSetSprintReviewRunAndReviewed round-trips the two review columns and rejects an
// unknown sprint (ErrNotFound) — the same contract as the planning columns.
func TestSetSprintReviewRunAndReviewed(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")

	if err := st.SetSprintReviewRun("SP1", "p1", "run-xyz"); err != nil {
		t.Fatalf("SetSprintReviewRun: %v", err)
	}
	if err := st.SetSprintReviewed("SP1", "p1"); err != nil {
		t.Fatalf("SetSprintReviewed: %v", err)
	}
	sps, _ := st.ListSprintsByProject("p1")
	if len(sps) != 1 || sps[0].ReviewRunID != "run-xyz" || sps[0].ReviewedAt == 0 {
		t.Fatalf("review columns not persisted: %+v", sps)
	}

	if err := st.SetSprintReviewRun("nope", "p1", "r"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("SetSprintReviewRun(unknown) = %v, want ErrNotFound", err)
	}
	if err := st.SetSprintReviewed("nope", "p1"); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("SetSprintReviewed(unknown) = %v, want ErrNotFound", err)
	}
}

// runReviewClose writes an optional corrections doc and runs the review_close step.
func runReviewClose(t *testing.T, st *tickets.Store, sprintID, correctionsDoc string) workflow.StepResult {
	t.Helper()
	dir := t.TempDir()
	inputs := map[string]any{"sprint_id": sprintID, "project_id": "p1"}
	if correctionsDoc != "" {
		if err := os.WriteFile(filepath.Join(dir, "CORRECTIONS.md"), []byte(correctionsDoc), 0o644); err != nil {
			t.Fatalf("write corrections: %v", err)
		}
		inputs["corrections"] = "CORRECTIONS.md"
	}
	r := &tickets.ReviewCloseRunner{Store: st}
	res, err := r.Run(context.Background(), workflow.Step{}, inputs, dir)
	if err != nil {
		t.Fatalf("runner returned a hard error (should be a failed StepResult): %v", err)
	}
	return res
}

// TestReviewCloseAccepted: no corrections doc → decision "accepted", reviewed_at
// stamped, a step.review_close event emitted.
func TestReviewCloseAccepted(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")

	res := runReviewClose(t, st, "SP1", "")
	if !res.Success {
		t.Fatalf("review_close should succeed: %s", res.Detail)
	}
	if res.Output["decision"] != tickets.ReviewAccepted {
		t.Fatalf("decision = %v, want %s", res.Output["decision"], tickets.ReviewAccepted)
	}
	if reviewedAt(t, st, "SP1") == 0 {
		t.Fatal("reviewed_at should be stamped after close")
	}
	if len(res.Events) != 1 || res.Events[0].Type != "step.review_close" {
		t.Fatalf("want one step.review_close event, got %+v", res.Events)
	}
}

// TestReviewCloseAcceptedWithCorrections: a corrections doc with stories → decision
// "accepted_with_corrections" carrying the correction ids.
func TestReviewCloseAcceptedWithCorrections(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")

	doc := "sprints:\n  - id: SP2\nstories:\n" +
		"  - id: FIX-1\n    title: Fix login\n    kind: bug\n    sprint_id: SP2\n" +
		"  - id: FIX-2\n    title: Add empty state\n    kind: story\n    sprint_id: SP2\n"
	res := runReviewClose(t, st, "SP1", doc)
	if !res.Success {
		t.Fatalf("review_close should succeed: %s", res.Detail)
	}
	if res.Output["decision"] != tickets.ReviewAcceptedWithCorrections {
		t.Fatalf("decision = %v, want %s", res.Output["decision"], tickets.ReviewAcceptedWithCorrections)
	}
	ids, _ := res.Output["corrections"].([]string)
	if len(ids) != 2 || ids[0] != "FIX-1" || ids[1] != "FIX-2" {
		t.Fatalf("correction ids = %v, want [FIX-1 FIX-2]", ids)
	}
	if reviewedAt(t, st, "SP1") == 0 {
		t.Fatal("reviewed_at should be stamped even on the corrections path")
	}
}

// TestReviewCloseMissingSprintID fails as a StepResult (never a hard error).
func TestReviewCloseMissingSprintID(t *testing.T) {
	st := openTemp(t)
	res := runReviewClose(t, st, "", "")
	if res.Success {
		t.Fatal("review_close without sprint_id should fail")
	}
}

// TestCorrectionsPublishAppendIdempotent covers the reject path's publish: a
// CORRECTIONS doc (bug/story targeting the NEXT sprint) is published once, creating
// the sprint if absent and carrying `kind`; re-publishing the same doc does NOT
// duplicate (the sprint-review loop republishes on every gate pass).
func TestCorrectionsPublishAppendIdempotent(t *testing.T) {
	st := openTemp(t)
	dir := t.TempDir()
	doc := "sprints:\n  - id: SP2\n    name: SP2\nstories:\n" +
		"  - id: FIX-1\n    title: Fix login\n    body: b\n    acceptance: a\n    kind: bug\n    sprint_id: SP2\n"
	if err := os.WriteFile(filepath.Join(dir, "CORRECTIONS.md"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := &tickets.PublishRunner{Store: st}
	inputs := map[string]any{"backlog": "CORRECTIONS.md", "project_id": "p1", "optional": true}

	first, err := r.Run(context.Background(), workflow.Step{}, inputs, dir)
	if err != nil || !first.Success {
		t.Fatalf("first publish: err=%v detail=%s", err, first.Detail)
	}
	if first.Output["created"] != 1 {
		t.Fatalf("first publish created = %v, want 1", first.Output["created"])
	}
	// The correction landed in the next sprint (created) and carries kind=bug.
	got, err := st.GetStoryInProject("p1", "FIX-1")
	if err != nil {
		t.Fatalf("get FIX-1: %v", err)
	}
	if got.SprintID != "SP2" {
		t.Errorf("FIX-1 sprint = %q, want SP2", got.SprintID)
	}
	if got.Kind != "bug" {
		t.Errorf("FIX-1 kind = %q, want bug", got.Kind)
	}

	// Re-publish the same doc → nothing new (idempotent by id).
	second, err := r.Run(context.Background(), workflow.Step{}, inputs, dir)
	if err != nil || !second.Success {
		t.Fatalf("second publish: err=%v detail=%s", err, second.Detail)
	}
	if second.Output["created"] != 0 || second.Output["skipped"] != 1 {
		t.Fatalf("re-publish should skip the existing story: created=%v skipped=%v", second.Output["created"], second.Output["skipped"])
	}
}

// TestPublishOptionalMissingFile: with optional=true a MISSING doc is a no-op success
// (the sprint-review happy path has no corrections doc), not a failure.
func TestPublishOptionalMissingFile(t *testing.T) {
	st := openTemp(t)
	r := &tickets.PublishRunner{Store: st}
	res, err := r.Run(context.Background(), workflow.Step{}, map[string]any{
		"backlog": "CORRECTIONS.md", "project_id": "p1", "optional": true,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("hard error: %v", err)
	}
	if !res.Success {
		t.Fatalf("optional missing file should succeed, got: %s", res.Detail)
	}
}
