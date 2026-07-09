// Package method applies a retrospective's approved method proposals to the registry.
// It is the retro ceremony's deterministic apply step (registry_apply) — the twin of
// plan_apply/review_close — the ONLY place the auto-improvement loop mutates the
// method, so its guardrails are the safety boundary of the whole feature:
//
//   - it may write ONLY under registry/{agents,skills,workflows}/ (enforced by an id
//     allowlist regex + kind→subdir map — no path escapes, no other trees);
//   - it may NEVER edit the retro's OWN method (the retro-analyst agent, the
//     retro-method skill, the retro workflow) — the method of the retro is not
//     self-editable; those meta-changes are made by a human by hand;
//   - every proposal is VALIDATED with the kernel's own parser BEFORE anything is
//     written (validate-all-then-apply-all): one unparseable proposal aborts the whole
//     apply naming it, with nothing written (atomic, like plan_apply);
//   - each applied proposal emits an auditable event (which file, which run, which
//     sprint) — the trail of who changed the method and why.
//
// On success it stamps the sprint's retro_at (unblocking the scheduler). An empty
// proposals block stamps retro_at and writes nothing — a retro that found no method
// problem still records completion.
package method

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"forge/internal/agent"
	"forge/internal/workflow"
	"gopkg.in/yaml.v3"
)

// Proposal kinds — the three registry file families a retro may edit.
const (
	kindAgent    = "agent"
	kindSkill    = "skill"
	kindWorkflow = "workflow"
)

// idRe bounds a proposal's id to a single safe path segment — no separators, no "..",
// so a proposal can never escape its kind's registry subdir (path-traversal guard).
var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// protected is the retro's own method, which registry_apply must never rewrite (keyed
// by "<kind>/<id>"). The retro cannot edit the analyst, its method skill, or its own
// workflow — meta-changes are a human's job (documented in retro-method.md).
var protected = map[string]bool{
	kindAgent + "/retro-analyst": true,
	kindSkill + "/retro-method":  true,
	kindWorkflow + "/retro":      true,
}

// SprintStamper is the ticket-store surface the runner needs: stamp retro_at. Satisfied
// by *tickets.Store.
type SprintStamper interface {
	SetSprintRetroed(sprintID, projectID string) error
}

// Invalidator drops a workflow id from the loader cache so an edited workflow is live
// on the next run. Satisfied by *workflow.Engine. Optional (nil = no-op).
type Invalidator interface {
	InvalidateWorkflow(id string)
}

// Proposal is one method edit: replace registry/<kind>/<id> with `content` (the WHOLE
// file — the registry validates and stores whole files, so a full replacement is
// robust where a textual diff would be fragile).
type Proposal struct {
	Kind      string `yaml:"kind"`
	ID        string `yaml:"id"`
	Rationale string `yaml:"rationale"`
	Evidence  string `yaml:"evidence"`
	Content   string `yaml:"content"`
}

type proposalDoc struct {
	Proposals []Proposal `yaml:"proposals"`
}

// ApplyRunner is the `registry_apply` step type.
type ApplyRunner struct {
	Tickets     SprintStamper
	RegistryDir string // the registry root; files land under <RegistryDir>/{agents,skills,workflows}/
	Engine      Invalidator
}

// Run implements workflow.Runner. Inputs: sprint_id (required), project_id (optional),
// retro (doc path; default docs/RETRO.md). Returns a FAILED StepResult (never an error)
// so a malformed doc / proposal surfaces on the run.
func (r *ApplyRunner) Run(_ context.Context, _ workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	sprintID := asString(inputs["sprint_id"])
	if sprintID == "" {
		return failResult("registry_apply: missing sprint_id"), nil
	}
	projectID := asString(inputs["project_id"])
	retroPath := "docs/RETRO.md"
	if v := asString(inputs["retro"]); v != "" {
		retroPath = v
	}
	runID := filepath.Base(workdir)

	proposals, err := readProposals(filepath.Join(workdir, retroPath))
	if err != nil {
		return failResult(fmt.Sprintf("registry_apply: %v", err)), nil
	}

	// Phase 1 — validate ALL (guardrails + parse). A failure here means NOTHING has
	// been written yet: the apply is atomic.
	for i, p := range proposals {
		if err := validateProposal(p); err != nil {
			return failResult(fmt.Sprintf("registry_apply: proposal %d (%s %q): %v", i+1, p.Kind, p.ID, err)), nil
		}
	}
	// Phase 2 — apply ALL, emitting an audit event per write.
	var events []workflow.ResultEvent
	for _, p := range proposals {
		path, werr := r.write(p)
		if werr != nil {
			return failResult(fmt.Sprintf("registry_apply: write %s %q: %v", p.Kind, p.ID, werr)), nil
		}
		if p.Kind == kindWorkflow && r.Engine != nil {
			r.Engine.InvalidateWorkflow(p.ID) // live for the next run
		}
		events = append(events, workflow.ResultEvent{Type: "step.registry_apply", Data: map[string]any{
			"kind": p.Kind, "id": p.ID, "path": path, "sprint_id": sprintID, "run_id": runID, "rationale": p.Rationale,
		}})
	}
	// Phase 3 — record the retro as applied so the scheduler advances the project.
	if err := r.Tickets.SetSprintRetroed(sprintID, projectID); err != nil {
		return failResult(fmt.Sprintf("registry_apply: stamp retro_at for %s: %v", sprintID, err)), nil
	}
	return workflow.StepResult{
		Success: true,
		Output:  map[string]any{"sprint_id": sprintID, "applied": len(proposals)},
		Detail:  fmt.Sprintf("registry_apply: sprint=%s applied=%d method edits", sprintID, len(proposals)),
		Events:  events,
	}, nil
}

// ValidateProposal is the exported guardrail check for a single method proposal,
// reused by the `validate` step type to lint a RETRO doc's proposals BEFORE they reach
// the human gate. It is the SAME check registry_apply applies at write time (kind/id/
// protected guardrails + content parse) — one rule, so a doc that passes validate is a
// doc registry_apply will accept.
func ValidateProposal(p Proposal) error { return validateProposal(p) }

// validateProposal enforces the guardrails and parses the proposed content with the
// kernel's own parser — read-only, so both registry_apply and the validate step can
// reject an illegal proposal before any write. It never touches disk.
func validateProposal(p Proposal) error {
	if !idRe.MatchString(p.ID) {
		return fmt.Errorf("%w: id must match %s (no path separators)", errInvalid, idRe.String())
	}
	if _, ok := kindSubdir[p.Kind]; !ok {
		return fmt.Errorf("%w: kind must be one of agent|skill|workflow", errInvalid)
	}
	if protected[p.Kind+"/"+p.ID] {
		return fmt.Errorf("%w: the retro's own method (%s %q) is not self-editable — change it by hand", errProtected, p.Kind, p.ID)
	}
	if strings.TrimSpace(p.Content) == "" {
		return fmt.Errorf("%w: empty content", errInvalid)
	}
	switch p.Kind {
	case kindWorkflow:
		if _, err := workflow.Parse([]byte(p.Content)); err != nil {
			return fmt.Errorf("%w: %v", errUnparseable, err)
		}
	case kindAgent:
		var m agent.Manifest
		if err := yaml.Unmarshal([]byte(p.Content), &m); err != nil {
			return fmt.Errorf("%w: %v", errUnparseable, err)
		}
		if m.Model == "" {
			return fmt.Errorf("%w: agent manifest must set a model", errInvalid)
		}
	case kindSkill:
		// Skills are free markdown — non-empty (checked above) is the only contract.
	}
	return nil
}

// kindSubdir maps a proposal kind to its registry subdir + file extension.
var kindSubdir = map[string]struct{ dir, ext string }{
	kindAgent:    {"agents", ".yaml"},
	kindSkill:    {"skills", ".md"},
	kindWorkflow: {"workflows", ".yaml"},
}

// write persists an already-validated proposal under the registry root and returns the
// registry-relative path written. The destination is built from the kind→subdir map +
// the id (already regex-guarded), so it can never escape registry/{agents,skills,workflows}/.
func (r *ApplyRunner) write(p Proposal) (string, error) {
	sub := kindSubdir[p.Kind]
	rel := filepath.Join(sub.dir, p.ID+sub.ext) // e.g. "agents/python-dev.yaml" — audit label
	full := filepath.Join(r.RegistryDir, sub.dir, p.ID+sub.ext)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}
