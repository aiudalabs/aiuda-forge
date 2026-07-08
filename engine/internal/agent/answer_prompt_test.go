package agent

import (
	"strings"
	"testing"

	"forge/internal/workflow"
)

// The `answers` input renders a dedicated section with the fixed preserve-in-place
// instruction — the behavioral difference from a feedback/reject round (which
// regenerates). When both are present, answers come BEFORE feedback.
func TestBuildPromptAnswersSection(t *testing.T) {
	m := &Manifest{Role: "You are the PRD writer."}
	step := workflow.Step{ID: "prd", Type: "agent"}

	prompt := buildPrompt(m, step, map[string]any{"answers": "Use Firebase, not Postgres."})

	if !strings.Contains(prompt, "## Answers to your open questions") {
		t.Fatalf("missing answers heading:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Use Firebase, not Postgres.") {
		t.Fatalf("missing answer text:\n%s", prompt)
	}
	const preserve = "Update the existing document incorporating these answers. Do NOT regenerate it from scratch; preserve everything not affected by the answers."
	if !strings.Contains(prompt, preserve) {
		t.Fatalf("missing the fixed preserve instruction:\n%s", prompt)
	}
	// answers must NOT be duplicated by the generic input loop (it is skipped there).
	if strings.Count(prompt, "Use Firebase, not Postgres.") != 1 {
		t.Fatalf("answers rendered more than once:\n%s", prompt)
	}
}

func TestBuildPromptAnswersBeforeFeedback(t *testing.T) {
	m := &Manifest{Role: "writer"}
	prompt := buildPrompt(m, workflow.Step{ID: "prd"}, map[string]any{
		"answers":  "ANSWER-TEXT",
		"feedback": "FEEDBACK-TEXT",
	})
	ai := strings.Index(prompt, "## Answers to your open questions")
	fi := strings.Index(prompt, "## Feedback from a previous attempt")
	if ai < 0 || fi < 0 {
		t.Fatalf("expected both sections, got answers@%d feedback@%d:\n%s", ai, fi, prompt)
	}
	if ai > fi {
		t.Fatalf("answers section must come BEFORE feedback:\n%s", prompt)
	}
}
