package tickets_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/tickets"
	"forge/internal/workflow"
)

// runPlan writes doc to <workdir>/PLAN.md and runs the plan_apply step against st for
// sprintID in project p1 (the project the shared mk* helpers use). Returns the result.
func runPlan(t *testing.T, st *tickets.Store, sprintID, doc string) workflow.StepResult {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	r := &tickets.PlanApplyRunner{Store: st}
	res, err := r.Run(context.Background(), workflow.Step{}, map[string]any{
		"sprint_id":  sprintID,
		"project_id": "p1",
		"plan":       "PLAN.md",
	}, dir)
	if err != nil {
		t.Fatalf("runner returned a hard error (should be a failed StepResult): %v", err)
	}
	return res
}

// yamlActions wraps an actions body in a fenced yaml block inside a markdown doc,
// mirroring what the planner persona writes.
func yamlActions(body string) string {
	return "# Sprint plan\n\n## Goal\nShip it.\n\n## Actions\n```yaml\n" + body + "\n```\n"
}

func storyIn(t *testing.T, st *tickets.Store, id string) tickets.Story {
	t.Helper()
	s, err := st.GetStoryInProject("p1", id)
	if err != nil {
		t.Fatalf("get story %s: %v", id, err)
	}
	return s
}

func plannedAt(t *testing.T, st *tickets.Store, sprintID string) int64 {
	t.Helper()
	sps, err := st.ListSprintsByProject("p1")
	if err != nil {
		t.Fatalf("list sprints: %v", err)
	}
	for _, sp := range sps {
		if sp.ID == sprintID {
			return sp.PlannedAt
		}
	}
	t.Fatalf("sprint %s not found", sprintID)
	return 0
}

func sprintExistsIn(t *testing.T, st *tickets.Store, id string) bool {
	t.Helper()
	sps, err := st.ListSprintsByProject("p1")
	if err != nil {
		t.Fatalf("list sprints: %v", err)
	}
	for _, sp := range sps {
		if sp.ID == id {
			return true
		}
	}
	return false
}

// TestPlanApplyValidSet applies a legal move + defer + edit and verifies each landed
// and the sprint is stamped planned.
func TestPlanApplyValidSet(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkSprint(t, st, "SP2")
	mkStory(t, st, "A", "SP1")
	mkStory(t, st, "B", "SP1", "A")
	mkStory(t, st, "C", "SP1")

	doc := yamlActions(strings.Join([]string{
		"actions:",
		"  - op: move",
		"    story_id: C",
		"    args:",
		"      sprint_id: SP2",
		"  - op: defer", // B is in SP1 → next existing sprint is SP2
		"    story_id: B",
		"  - op: edit",
		"    story_id: A",
		"    args:",
		"      title: A-renamed",
	}, "\n"))

	res := runPlan(t, st, "SP1", doc)
	if !res.Success {
		t.Fatalf("expected success, got failed: %s", res.Detail)
	}
	if got := storyIn(t, st, "C").SprintID; got != "SP2" {
		t.Errorf("C sprint = %q, want SP2", got)
	}
	if got := storyIn(t, st, "B").SprintID; got != "SP2" {
		t.Errorf("B (deferred) sprint = %q, want SP2", got)
	}
	if got := storyIn(t, st, "A").Title; got != "A-renamed" {
		t.Errorf("A title = %q, want A-renamed", got)
	}
	if plannedAt(t, st, "SP1") == 0 {
		t.Error("SP1 planned_at should be set after a successful apply")
	}
}

// TestPlanApplyDeferCreatesSprint verifies defer synthesizes the next sprint when the
// story is already in the last one.
func TestPlanApplyDeferCreatesSprint(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkStory(t, st, "A", "SP1")

	res := runPlan(t, st, "SP1", yamlActions("actions:\n  - op: defer\n    story_id: A"))
	if !res.Success {
		t.Fatalf("expected success, got: %s", res.Detail)
	}
	if got := storyIn(t, st, "A").SprintID; got != "SP2" {
		t.Errorf("A sprint = %q, want SP2 (synthesized)", got)
	}
	if !sprintExistsIn(t, st, "SP2") {
		t.Error("defer should have created sprint SP2")
	}
}

// TestPlanApplyIllegalAtomic is the atomicity contract: a legal action followed by an
// illegal one applies NEITHER (the illegal one is caught before any apply), and the
// sprint is NOT stamped planned.
func TestPlanApplyIllegalAtomic(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	if err := st.CreateStory(tickets.Story{ID: "A", SprintID: "SP1", ProjectID: "p1", Title: "A-original"}); err != nil {
		t.Fatal(err)
	}
	mkStory(t, st, "B", "SP1")

	// action[0] legal (would rename A); action[1] illegal (move B to a missing sprint).
	doc := yamlActions(strings.Join([]string{
		"actions:",
		"  - op: edit",
		"    story_id: A",
		"    args:",
		"      title: A-CHANGED",
		"  - op: move",
		"    story_id: B",
		"    args:",
		"      sprint_id: DOES-NOT-EXIST",
	}, "\n"))

	res := runPlan(t, st, "SP1", doc)
	if res.Success {
		t.Fatal("expected failure for an illegal action")
	}
	if got := storyIn(t, st, "A").Title; got != "A-original" {
		t.Errorf("A title = %q, want A-original — the legal action must NOT have applied", got)
	}
	if plannedAt(t, st, "SP1") != 0 {
		t.Error("SP1 planned_at must remain 0 after an aborted plan")
	}
}

// TestPlanApplyUnknownOp rejects an unrecognized op with nothing applied.
func TestPlanApplyUnknownOp(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkStory(t, st, "A", "SP1")

	res := runPlan(t, st, "SP1", yamlActions("actions:\n  - op: frobnicate\n    story_id: A"))
	if res.Success {
		t.Fatal("expected failure for an unknown op")
	}
	if !strings.Contains(res.Detail, "unknown op") {
		t.Errorf("detail should name the unknown op, got: %s", res.Detail)
	}
	if plannedAt(t, st, "SP1") != 0 {
		t.Error("SP1 planned_at must remain 0")
	}
}

// TestPlanApplyEmptyActions confirms a no-op plan is valid and just stamps planned_at.
func TestPlanApplyEmptyActions(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkStory(t, st, "A", "SP1")

	res := runPlan(t, st, "SP1", yamlActions("actions: []"))
	if !res.Success {
		t.Fatalf("empty actions should succeed, got: %s", res.Detail)
	}
	if plannedAt(t, st, "SP1") == 0 {
		t.Error("SP1 planned_at should be set even for a no-op (confirmation) plan")
	}
}

// TestPlanApplyNoActionsBlock fails a doc with no actions block at all.
func TestPlanApplyNoActionsBlock(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	res := runPlan(t, st, "SP1", "# Sprint plan\n\nJust prose, no actions.\n")
	if res.Success {
		t.Fatal("expected failure when no actions block is present")
	}
	if plannedAt(t, st, "SP1") != 0 {
		t.Error("SP1 planned_at must remain 0")
	}
}

// TestPlanApplySplit replaces a story with parts and cancels the original.
func TestPlanApplySplit(t *testing.T) {
	st := openTemp(t)
	mkSprint(t, st, "SP1")
	mkStory(t, st, "A", "SP1")

	doc := yamlActions(strings.Join([]string{
		"actions:",
		"  - op: split",
		"    story_id: A",
		"    args:",
		"      parts:",
		"        - id: A-1",
		"          title: Part one",
		"          acceptance: does one thing",
		"        - id: A-2",
		"          title: Part two",
		"          acceptance: does another",
	}, "\n"))

	res := runPlan(t, st, "SP1", doc)
	if !res.Success {
		t.Fatalf("split should succeed, got: %s", res.Detail)
	}
	if got := storyIn(t, st, "A").Status; got != tickets.StatusCancelled {
		t.Errorf("original A status = %q, want cancelled", got)
	}
	if got := storyIn(t, st, "A-1").SprintID; got != "SP1" {
		t.Errorf("part A-1 sprint = %q, want SP1 (inherited)", got)
	}
	if got := storyIn(t, st, "A-2").Title; got != "Part two" {
		t.Errorf("part A-2 title = %q, want 'Part two'", got)
	}
	if plannedAt(t, st, "SP1") == 0 {
		t.Error("SP1 planned_at should be set after a successful split")
	}
}
