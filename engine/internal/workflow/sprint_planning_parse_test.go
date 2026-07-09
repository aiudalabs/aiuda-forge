package workflow

import (
	"path/filepath"
	"testing"
)

// TestSprintPlanningYAMLParses validates the sprint-planning ceremony workflow:
// plan (design/planner) → plan_gate (human, loops back to plan on reject/answer) →
// apply (plan_apply). It must parse with the kernel loader and reference real targets.
func TestSprintPlanningYAMLParses(t *testing.T) {
	path := filepath.Join("..", "..", "registry", "workflows", "sprint-planning.yaml")
	wf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("sprint-planning.yaml failed to parse: %v", err)
	}
	if wf.ID != "sprint-planning" {
		t.Errorf("id: got %q, want sprint-planning", wf.ID)
	}
	if len(wf.Steps) != 4 {
		t.Fatalf("step count: got %d, want 4 (plan, validate_plan, plan_gate, apply)", len(wf.Steps))
	}
	if wf.Steps[0].ID != "plan" || wf.Steps[0].Type != "design" || wf.Steps[0].Agent != "planner" {
		t.Errorf("step 0: got id=%s type=%s agent=%s, want plan/design/planner",
			wf.Steps[0].ID, wf.Steps[0].Type, wf.Steps[0].Agent)
	}
	// validate_plan lints the plan's actions BEFORE the human gate; a malformed plan
	// loops back to plan.
	val := wf.Steps[1]
	if val.ID != "validate_plan" || val.Type != "validate" || val.OnFail == nil || val.OnFail.Goto != "plan" {
		t.Errorf("step 1: want validate_plan/validate looping back to plan, got id=%s type=%s onfail=%+v", val.ID, val.Type, val.OnFail)
	}
	gate := wf.Steps[2]
	if gate.Type != "human_gate" || gate.OnFail == nil || gate.OnFail.Goto != "plan" {
		// on_fail.goto=plan is required for the conversational `answer` verb to feed
		// answers back into the plan step (#22).
		t.Errorf("plan_gate must loop back to plan on reject/answer, got %+v", gate.OnFail)
	}
	last := wf.Steps[3]
	if last.ID != "apply" || last.Type != "plan_apply" {
		t.Errorf("last step: got id=%s type=%s, want apply/plan_apply", last.ID, last.Type)
	}
}
