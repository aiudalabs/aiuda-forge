package tickets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forge/internal/workflow"
	"gopkg.in/yaml.v3"
)

// Sprint-review decisions recorded by review_close. "accepted" = the increment was
// approved as-is; "accepted_with_corrections" = approved after the reviewer's
// feedback was turned into correction stories (published to the next sprint).
const (
	ReviewAccepted                = "accepted"
	ReviewAcceptedWithCorrections = "accepted_with_corrections"
)

// ReviewCloseRunner is the `review_close` step type — the sprint-review ceremony's
// deterministic apply step (the plan_apply twin for review). It stamps the reviewed
// sprint's reviewed_at (the signal that unblocks the scheduler from advancing to the
// next sprint) and records the acceptance decision — accepted, or
// accepted_with_corrections plus the ids of the correction stories created — as a run
// event. Like plan_apply it holds the Store directly and returns a FAILED StepResult
// (never an error) so a malformed input surfaces on the run rather than panicking.
type ReviewCloseRunner struct {
	Store *Store
}

// Run implements workflow.Runner. Inputs: sprint_id (required), project_id
// (optional; defaults to the default project), corrections (optional doc path — the
// corrections backlog the review's reject path produced; its story ids drive the
// decision. Absent or empty = a plain acceptance).
func (r *ReviewCloseRunner) Run(_ context.Context, _ workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	sprintID := asString(inputs["sprint_id"])
	if sprintID == "" {
		return failResult("review_close: missing sprint_id"), nil
	}
	projectID := asString(inputs["project_id"])

	// The corrections story ids (if any) determine the decision. The doc is absent on
	// the plain-acceptance path (no reject ever happened) — that is not an error.
	correctionIDs, err := reviewCorrectionIDs(workdir, asString(inputs["corrections"]))
	if err != nil {
		return failResult(fmt.Sprintf("review_close: %v", err)), nil
	}

	decision := ReviewAccepted
	if len(correctionIDs) > 0 {
		decision = ReviewAcceptedWithCorrections
	}

	// Stamp reviewed_at LAST — after the decision is computed — so a failure to read
	// the corrections doc never leaves the sprint marked reviewed on a bad close.
	if err := r.Store.SetSprintReviewed(sprintID, projectID); err != nil {
		return failResult(fmt.Sprintf("review_close: stamp reviewed_at for %s: %v", sprintID, err)), nil
	}

	data := map[string]any{
		"sprint_id":   sprintID,
		"decision":    decision,
		"corrections": correctionIDs,
	}
	detail := fmt.Sprintf("review_close: sprint=%s %s", sprintID, decision)
	if len(correctionIDs) > 0 {
		detail += fmt.Sprintf(" (corrections: %d)", len(correctionIDs))
	}
	return workflow.StepResult{
		Success: true,
		Output:  data,
		Detail:  detail,
		Events:  []workflow.ResultEvent{{Type: "step.review_close", Data: data}},
	}, nil
}

// reviewCorrectionIDs returns the story ids declared in the corrections doc at
// workdir/correctionsPath. An empty path, a missing file, or an empty `stories:`
// list all yield an empty slice (no corrections — the plain-acceptance case). A
// present-but-malformed doc is an error so a real parse failure is not swallowed.
func reviewCorrectionIDs(workdir, correctionsPath string) ([]string, error) {
	if correctionsPath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(workdir, correctionsPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no corrections doc → plain acceptance
		}
		return nil, fmt.Errorf("read %s: %w", correctionsPath, err)
	}
	var bf BacklogFile
	if err := yaml.Unmarshal(raw, &bf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", correctionsPath, err)
	}
	var ids []string
	for _, st := range bf.Stories {
		if st.ID != "" {
			ids = append(ids, st.ID)
		}
	}
	return ids, nil
}
