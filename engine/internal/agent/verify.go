package agent

import (
	"context"
	"strings"
	"time"

	"forge/internal/store"
	"forge/internal/workflow"
)

// VerifyRunner implements the `agentic_verify` step type: it spawns a FRESH
// verifier agent — typically a DIFFERENT model than the implementer — that
// exercises the change and returns a verdict (works|broken) plus evidence. A
// "broken" verdict fails the step, so a downstream on_fail{goto: implement}
// loops back. Verification is agentic ("does it work?"), not just "do tests pass?".
type VerifyRunner struct {
	Backend Backend
	Agents  Loader
	Auth    Auth
	Timeout time.Duration
}

// NewVerifyRunner builds an agentic_verify runner.
func NewVerifyRunner(backend Backend, agents Loader) *VerifyRunner {
	return &VerifyRunner{Backend: backend, Agents: agents, Timeout: 15 * time.Minute}
}

// Run implements workflow.Runner.
func (r *VerifyRunner) Run(ctx context.Context, step workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	agentID := step.Agent
	if agentID == "" {
		agentID = "verifier"
	}
	manifest, err := r.Agents.Load(agentID)
	if err != nil {
		return workflow.StepResult{Success: false, Detail: "load verifier: " + err.Error()}, nil
	}
	model := step.Model // cross-model: a fresh, different model verifies
	if model == "" {
		model = manifest.Model
	}

	prompt := buildPrompt(manifest, step, inputs) +
		"\n\nWhen done, end your reply with a line exactly: `VERDICT: works` or `VERDICT: broken`," +
		" followed by one line of evidence."

	res, runErr := r.Backend.Run(ctx, prompt, Options{
		Model:        model,
		AllowedTools: manifest.AllowedTools(),
		SystemPrompt: manifest.Persona,
		Workdir:      workdir,
		Timeout:      r.Timeout,
		Auth:         r.Auth,
	}, nil)
	if runErr != nil {
		return workflow.StepResult{Success: false, Detail: "verifier error: " + runErr.Error()}, nil
	}

	verdict, evidence := parseVerdict(res.Text, res.Success)
	works := verdict == "works"
	return workflow.StepResult{
		Success: works,
		Output:  map[string]any{"verdict": verdict, "evidence": evidence, "model": model, "agent": manifest.ID},
		Detail:  "verify: " + verdict + " — " + evidence,
		Events: []workflow.ResultEvent{{
			Type: store.EventStepVerify,
			Data: map[string]any{"step": step.ID, "verdict": verdict, "evidence": evidence},
		}},
	}, nil
}

// parseVerdict extracts works|broken from the verifier's text. If no explicit
// VERDICT line is present, fall back to the backend success flag (so a fake that
// just succeeds reads as "works").
func parseVerdict(text string, backendSuccess bool) (verdict, evidence string) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "verdict: broken"), strings.Contains(lower, "verdict:broken"):
		verdict = "broken"
	case strings.Contains(lower, "verdict: works"), strings.Contains(lower, "verdict:works"):
		verdict = "works"
	case strings.Contains(lower, "broken"):
		verdict = "broken"
	case strings.Contains(lower, "works"):
		verdict = "works"
	default:
		if backendSuccess {
			verdict = "works"
		} else {
			verdict = "broken"
		}
	}
	// Evidence = last non-empty line.
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			evidence = s
			break
		}
	}
	return verdict, evidence
}

// HumanGateRunner implements the `human_gate` step type: it PARKS the run for a
// human decision (high-risk approval / business gate) and emits
// run.awaiting_approval. The run resumes only via POST /runs/{id}/steps/{step}/approve.
type HumanGateRunner struct{}

// Run implements workflow.Runner by signalling Park.
func (HumanGateRunner) Run(_ context.Context, step workflow.Step, inputs map[string]any, _ string) (workflow.StepResult, error) {
	reason := asString(inputs["reason"])
	if reason == "" {
		reason = "awaiting human approval"
	}
	return workflow.StepResult{Park: true, Detail: reason, Output: map[string]any{"awaiting": true}}, nil
}
