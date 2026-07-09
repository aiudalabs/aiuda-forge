// Package validate is the `validate` step type: a deterministic, LLM-free lint of a
// design/ceremony spec doc that runs BETWEEN a phase and its human gate. Its purpose is
// to kill the class of bug "a malformed doc slips past the human reviewer and breaks
// something downstream" BEFORE it costs the reviewer's attention — the doc a human sees
// already parses and satisfies its schema's structural contract.
//
// It is deliberately dumb and cheap (no model, no store, no network): read the doc,
// apply the schema's rules, and return a FAILED StepResult (never an error) whose Detail
// names the schema + the offending field/line + what was expected — so the on_fail loop
// in the workflow bounces the phase back with an actionable message. The plan and retro
// schemas reuse the EXACT parsers/validators of plan_apply (tickets) and registry_apply
// (method) — there is one parser per artifact, never a second copy that can drift.
package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"forge/internal/method"
	"forge/internal/tickets"
	"forge/internal/workflow"
	"gopkg.in/yaml.v3"
)

// Runner implements workflow.Runner for step type `validate`. AgentsDir is the
// registry agents directory used to derive the set of known lanes for the backlog
// owner check; when empty the lane check is skipped (a spec doc still validates for
// every other rule). It is set by app.Build to <registry>/agents.
type Runner struct {
	AgentsDir string
}

// Run implements workflow.Runner. Inputs: schema (required: prd|backlog|provisioning|
// plan|retro) and path (the doc to lint, relative to the run workdir). It returns a
// FAILED StepResult — never an error — so a malformed doc surfaces on the run and the
// workflow's on_fail loops the phase back, instead of panicking the executor.
func (r *Runner) Run(_ context.Context, _ workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	schema := strings.TrimSpace(asString(inputs["schema"]))
	if schema == "" {
		return fail("validate: missing schema (prd|backlog|provisioning|plan|retro)"), nil
	}
	path := strings.TrimSpace(asString(inputs["path"]))
	if path == "" {
		return fail(fmt.Sprintf("validate %s: missing path", schema)), nil
	}
	raw, err := os.ReadFile(filepath.Join(workdir, path))
	if err != nil {
		return fail(fmt.Sprintf("validate %s: read %s: %v", schema, path, err)), nil
	}

	if verr := r.check(schema, raw); verr != nil {
		return fail(fmt.Sprintf("validate %s (%s): %v", schema, path, verr)), nil
	}
	return workflow.StepResult{
		Success: true,
		Output:  map[string]any{"schema": schema, "path": path, "valid": true},
		Detail:  fmt.Sprintf("validate: %s %s OK", schema, path),
	}, nil
}

// check dispatches to the per-schema linter. An unknown schema is itself a validation
// failure (a workflow typo should be loud, not silently pass).
func (r *Runner) check(schema string, raw []byte) error {
	switch schema {
	case "prd":
		return checkPRD(raw)
	case "backlog":
		return r.checkBacklog(raw)
	case "provisioning":
		return checkProvisioning(raw)
	case "plan":
		return checkPlan(raw)
	case "retro":
		return checkRetro(raw)
	default:
		return fmt.Errorf("unknown schema %q (expected prd|backlog|provisioning|plan|retro)", schema)
	}
}

// ── prd ────────────────────────────────────────────────────────────────────────

// frDefRe matches a functional-requirement DEFINITION line as the prd-template writes
// it: an optional list bullet, then a bolded `FR-<n>` id (e.g. "- **FR-01 [P0]**:").
// Anchored to line start + bold so a mid-prose reference ("see FR-01") is not counted
// as a requirement.
var frDefRe = regexp.MustCompile(`(?im)^\s*[-*]?\s*\*\*\s*(FR-\d+)`)

// h1to3Re matches a markdown H1–H3 heading (NOT H4 `#### Epic`, which is where FRs
// nest). It bounds the LAST requirement's scope at the next section so it cannot borrow
// a GIVEN/WHEN/THEN from an unrelated later section (e.g. §5 User Stories).
var h1to3Re = regexp.MustCompile(`(?m)^#{1,3}\s`)

var (
	givenRe = regexp.MustCompile(`(?i)\bgiven\b`)
	whenRe  = regexp.MustCompile(`(?i)\bwhen\b`)
	thenRe  = regexp.MustCompile(`(?i)\bthen\b`)
)

// checkPRD enforces the prd-template contract that matters downstream: every functional
// requirement (FR-NN) carries ≥1 GIVEN/WHEN/THEN scenario — that scenario is the
// falsifiable acceptance contract the story inherits, so an FR without one produces a
// story with nothing to verify.
func checkPRD(raw []byte) error {
	doc := string(raw)
	locs := frDefRe.FindAllStringSubmatchIndex(doc, -1)
	if len(locs) == 0 {
		return fmt.Errorf("no functional requirement found — a PRD must list requirements as `**FR-NN**` each with a GIVEN/WHEN/THEN scenario")
	}
	headings := h1to3Re.FindAllStringIndex(doc, -1)
	var missing []string
	for i, loc := range locs {
		start := loc[0]
		id := doc[loc[2]:loc[3]] // submatch 1 = the FR id
		end := len(doc)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		// Cap the scope at the next H1–H3 heading after this FR so the last FR does not
		// absorb GWT keywords from a later section.
		for _, h := range headings {
			if h[0] > start && h[0] < end {
				end = h[0]
				break
			}
		}
		seg := doc[start:end]
		if !(givenRe.MatchString(seg) && whenRe.MatchString(seg) && thenRe.MatchString(seg)) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("requirement(s) without a complete GIVEN/WHEN/THEN scenario: %s — each FR needs a falsifiable acceptance scenario", strings.Join(missing, ", "))
	}
	return nil
}

// ── backlog ──────────────────────────────────────────────────────────────────────

// frontendLaneRe matches the owner lanes that build user-facing screens (per the
// scrum-master persona: react-dev / flutter-dev). A story owned by one of these that
// builds a screen carries a screen_key — the key that lets ui-verify's art-director
// judge it against its mockup. Missing it means that screen silently escapes visual QA.
var frontendLaneRe = regexp.MustCompile(`(?i)\b(react-dev|flutter-dev)\b`)

// checkBacklog lints docs/backlog.yaml against the ticket store's structural contract,
// reusing tickets.BacklogFile so the schema never diverges from what ticket_publish
// reads: valid YAML; unique story ids; deps reference existing ids; owner is a known
// lane; frontend stories carry a screen_key.
func (r *Runner) checkBacklog(raw []byte) error {
	var bf tickets.BacklogFile
	if err := yaml.Unmarshal(raw, &bf); err != nil {
		return fmt.Errorf("YAML does not parse: %v", err)
	}
	// Unique ids (build the id set as we go for the dep check).
	ids := map[string]bool{}
	for _, s := range bf.Stories {
		if s.ID == "" {
			return fmt.Errorf("a story has an empty id")
		}
		if ids[s.ID] {
			return fmt.Errorf("duplicate story id %q", s.ID)
		}
		ids[s.ID] = true
	}
	// Deps must reference existing story ids. deps mirrors backlogStory.deps(): the
	// `deps` list, or the `depends_on` alias when `deps` is absent.
	for _, s := range bf.Stories {
		deps := s.Deps
		if len(deps) == 0 {
			deps = s.DependsOn
		}
		for _, dep := range deps {
			if !ids[dep] {
				return fmt.Errorf("story %q depends on %q which is not a story in this backlog", s.ID, dep)
			}
		}
	}
	// Owner must be a known lane (a registry agent id) — an unknown owner cannot be
	// routed to an executor. Skipped when the lane set is unavailable.
	lanes := r.knownLanes()
	if len(lanes) > 0 {
		for _, s := range bf.Stories {
			if s.Owner == "" {
				return fmt.Errorf("story %q has no owner (lane)", s.ID)
			}
			if !lanes[strings.ToLower(s.Owner)] {
				return fmt.Errorf("story %q owner %q is not a known lane (registry agent id); known: %s", s.ID, s.Owner, strings.Join(sortedKeys(lanes), ", "))
			}
		}
	}
	// Frontend stories must carry a screen_key (the anchor the art-director QA needs).
	for _, s := range bf.Stories {
		if frontendLaneRe.MatchString(s.Owner) && strings.TrimSpace(s.ScreenKey) == "" {
			return fmt.Errorf("frontend story %q (owner %q) has no screen_key — a screen-building story needs one so its UI can be verified against its mockup", s.ID, s.Owner)
		}
	}
	return nil
}

// knownLanes reads the registry agents directory and returns the set of agent ids
// (lowercased) — the valid story owners. An unreadable/empty dir returns an empty set,
// which disables the lane check rather than failing every backlog.
func (r *Runner) knownLanes() map[string]bool {
	if r.AgentsDir == "" {
		return nil
	}
	entries, err := os.ReadDir(r.AgentsDir)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yaml")
		out[strings.ToLower(id)] = true
	}
	return out
}

// ── provisioning ───────────────────────────────────────────────────────────────

// provisioningKeys are the top-level keys the architect's boundary contract
// (docs/provisioning.yaml) must declare — the downstream provisioning-linter diffs the
// code's actual usage against every one of them, so a missing block is a silent hole.
// The architect persona mandates all of these present (use [] / derive:true when a
// block is empty — never omit a key).
var provisioningKeys = []string{"version", "roles", "indexes", "dependencies", "services", "authz", "bootstrap"}

// checkProvisioning enforces that docs/provisioning.yaml is valid YAML and declares the
// architect's contract keys. It does NOT judge the CONTENTS (that is the stack-specific
// provisioning-linter's job against the real code) — only that the declared contract is
// structurally complete.
func checkProvisioning(raw []byte) error {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("YAML does not parse: %v", err)
	}
	if doc == nil {
		return fmt.Errorf("empty document — expected the boundary-contract keys: %s", strings.Join(provisioningKeys, ", "))
	}
	var missing []string
	for _, k := range provisioningKeys {
		if _, ok := doc[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing contract key(s): %s (declare every block — use [] / derive:true when empty, never omit)", strings.Join(missing, ", "))
	}
	// Light shape checks on the keys whose type is contracted: indexes is a mapping
	// (derive + required); the rest are lists. A null/empty value is always allowed.
	if v := doc["indexes"]; v != nil {
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("`indexes` must be a mapping (derive: / required:), got %T", v)
		}
	}
	for _, k := range []string{"roles", "dependencies", "services", "authz", "bootstrap"} {
		if v := doc[k]; v != nil {
			if _, ok := v.([]any); !ok {
				return fmt.Errorf("`%s` must be a list (use [] when empty), got %T", k, v)
			}
		}
	}
	return nil
}

// ── plan (reuses plan_apply's parser) ────────────────────────────────────────────

// checkPlan lints a sprint-planning PLAN doc's `actions:` block by reusing plan_apply's
// exact parser and op set (tickets.ParseActions / tickets.IsKnownPlanOp) — the same code
// that will apply the plan — so a plan that validates is a plan plan_apply can parse.
func checkPlan(raw []byte) error {
	actions, err := tickets.ParseActions(raw)
	if err != nil {
		return fmt.Errorf("actions block: %v", err)
	}
	for i, a := range actions {
		if !tickets.IsKnownPlanOp(a.Op) {
			return fmt.Errorf("action %d: unknown op %q (expected move|defer|cancel|edit|split)", i+1, a.Op)
		}
		if strings.TrimSpace(a.StoryID) == "" {
			return fmt.Errorf("action %d (%s): missing story_id", i+1, a.Op)
		}
	}
	return nil
}

// ── retro (reuses registry_apply's parser + guardrails) ──────────────────────────

// checkRetro lints a RETRO doc's `proposals:` block by reusing registry_apply's exact
// parser and guardrail check (method.ParseProposals / method.ValidateProposal) — the
// same rules the apply step enforces at write time (legal kind, path-safe id, not the
// retro's own protected method, parseable content). One rule, no drift.
func checkRetro(raw []byte) error {
	proposals, err := method.ParseProposals(raw)
	if err != nil {
		return fmt.Errorf("proposals block: %v", err)
	}
	for i, p := range proposals {
		if err := method.ValidateProposal(p); err != nil {
			return fmt.Errorf("proposal %d (%s %q): %v", i+1, p.Kind, p.ID, err)
		}
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────────

func fail(detail string) workflow.StepResult {
	return workflow.StepResult{Success: false, Detail: detail}
}

// asString renders a plumbed input as a string (mirrors the tickets/method runners).
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
