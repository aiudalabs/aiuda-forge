package tickets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"forge/internal/workflow"
	"gopkg.in/yaml.v3"
)

// Plan action ops — the sprint-planning primitives a planner may emit in a
// PLAN-SP<n>.md `actions:` block. They map 1:1 to the store's team-move mutators.
const (
	planOpMove   = "move"   // reassign a story to args.sprint_id
	planOpDefer  = "defer"  // move a story to the NEXT sprint (created if absent)
	planOpCancel = "cancel" // abandon a story (backlog/failed/in_review only)
	planOpEdit   = "edit"   // patch a story's fields/deps
	planOpSplit  = "split"  // replace a story with args.parts (≥2)
)

// PlanAction is one entry of a plan's `actions:` list. Args carries the op-specific
// arguments; only the fields relevant to Op are read.
type PlanAction struct {
	Op      string         `yaml:"op"`
	StoryID string         `yaml:"story_id"`
	Args    PlanActionArgs `yaml:"args"`
}

// PlanActionArgs is the union of every op's arguments. A given op reads only its
// own fields (move→SprintID; edit→the patch fields; split→Parts); the rest are
// ignored. Edit fields are pointers so "absent" (nil) is distinct from "set to
// empty", matching StoryPatch's sparse-update semantics.
type PlanActionArgs struct {
	SprintID   string     `yaml:"sprint_id"`  // move
	Title      *string    `yaml:"title"`      // edit
	Body       *string    `yaml:"body"`       // edit
	Acceptance *string    `yaml:"acceptance"` // edit
	Owner      *string    `yaml:"owner"`      // edit
	ScreenKey  *string    `yaml:"screen_key"` // edit
	Deps       *[]string  `yaml:"deps"`       // edit (replaces the dep set)
	Parts      []planPart `yaml:"parts"`      // split
}

// planPart is one new story produced by a split. Its own yaml keys (acceptance,
// not accept) are chosen to read naturally in a planner-authored doc; it is mapped
// to a StoryDraft at apply time.
type planPart struct {
	ID         string `yaml:"id"`
	Title      string `yaml:"title"`
	Body       string `yaml:"body"`
	Acceptance string `yaml:"acceptance"`
	Owner      string `yaml:"owner"`
}

// planDoc is the yaml shape ApplyPlan parses out of a PLAN doc: a single
// `actions:` list (plus an optional free-text goal the store ignores).
type planDoc struct {
	Actions []PlanAction `yaml:"actions"`
}

// ApplyPlan executes a sprint-planning plan against the store: it VALIDATES every
// action first, applies NONE if any is illegal, then applies them in order and
// stamps the sprint planned_at. The validate-first pass is what makes an illegal
// action abort with nothing applied (the ceremony's atomicity contract): the
// store's per-mutator transactions each stay all-or-nothing, and validation catches
// an illegal transition / broken dep / unknown op BEFORE the first mutation runs.
//
// (A single cross-action DB transaction is not used deliberately: the store runs on
// one connection — SetMaxOpenConns(1) — so the mutators, which take that connection
// themselves, cannot be composed inside an outer tx without deadlocking. Because a
// plannable sprint's stories are all still `backlog` and planning runs on a
// not-yet-fired sprint under human approval, the residual validate→apply race is a
// non-issue in practice; a mid-apply failure returns an error and leaves planned_at
// unset so the plan can be re-approved.)
//
// An empty action list is valid: it records "the plan is confirmed as designed" by
// stamping planned_at with no mutations. projectID empty resolves to the default
// project.
func (s *Store) ApplyPlan(projectID, sprintID string, actions []PlanAction) error {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	// Phase 1 — validate all. A failure here means NOTHING has been applied yet.
	for i, a := range actions {
		if err := s.validatePlanAction(projectID, a); err != nil {
			return fmt.Errorf("action %d (%s %s): %w", i+1, a.Op, a.StoryID, err)
		}
	}
	// Phase 2 — apply all. Each mutator is individually atomic.
	for i, a := range actions {
		if err := s.applyPlanAction(projectID, a); err != nil {
			return fmt.Errorf("action %d apply (%s %s): %w", i+1, a.Op, a.StoryID, err)
		}
	}
	// Phase 3 — record the plan as applied so the scheduler may fire the sprint.
	return s.SetSprintPlanned(sprintID, projectID)
}

// validatePlanAction mirrors the precondition each mutator enforces, read-only, so
// ApplyPlan can reject an illegal action before any mutation runs. It never writes.
func (s *Store) validatePlanAction(projectID string, a PlanAction) error {
	switch a.Op {
	case planOpMove:
		st, err := s.getStory(projectID, a.StoryID)
		if err != nil {
			return err
		}
		if st.Status != StatusBacklog && st.Status != StatusFailed {
			return fmt.Errorf("%w: %s is %s (move allowed only from backlog/failed)", ErrInvalidState, a.StoryID, st.Status)
		}
		if a.Args.SprintID != "" {
			ok, err := s.sprintExists(a.Args.SprintID, st.ProjectID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: sprint %s", ErrNotFound, a.Args.SprintID)
			}
		}
		return s.checkMoveOrdering(st, a.Args.SprintID)

	case planOpDefer:
		st, err := s.getStory(projectID, a.StoryID)
		if err != nil {
			return err
		}
		if st.Status != StatusBacklog && st.Status != StatusFailed {
			return fmt.Errorf("%w: %s is %s (defer allowed only from backlog/failed)", ErrInvalidState, a.StoryID, st.Status)
		}
		if st.SprintID == "" {
			return fmt.Errorf("%w: %s has no sprint to defer from", ErrInvalidInput, a.StoryID)
		}
		target, _, err := s.deferTarget(st)
		if err != nil {
			return err
		}
		return s.checkMoveOrdering(st, target)

	case planOpCancel:
		st, err := s.getStory(projectID, a.StoryID)
		if err != nil {
			return err
		}
		// legalSources[cancelled] = backlog/failed/in_review.
		if st.Status != StatusBacklog && st.Status != StatusFailed && st.Status != StatusInReview {
			return fmt.Errorf("%w: %s is %s (cancel allowed only from backlog/failed/in_review)", ErrInvalidState, a.StoryID, st.Status)
		}
		return nil

	case planOpEdit:
		st, err := s.getStory(projectID, a.StoryID)
		if err != nil {
			return err
		}
		if !editable(st.Status) {
			return fmt.Errorf("%w: %s is %s (edit allowed only in backlog/failed/in_review)", ErrInvalidState, a.StoryID, st.Status)
		}
		if a.Args.Deps != nil {
			for _, dep := range *a.Args.Deps {
				if dep == a.StoryID {
					return fmt.Errorf("%w: %s depends on itself", ErrDepCycle, a.StoryID)
				}
				ok, err := s.storyExists(dep, st.ProjectID)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("%w: %s -> %s", ErrDepNotFound, a.StoryID, dep)
				}
			}
			if err := s.checkNoCycle(a.StoryID, *a.Args.Deps, st.ProjectID); err != nil {
				return err
			}
		}
		return nil

	case planOpSplit:
		orig, err := s.getStory(projectID, a.StoryID)
		if err != nil {
			return err
		}
		if orig.Status != StatusBacklog && orig.Status != StatusFailed {
			return fmt.Errorf("%w: %s is %s (split allowed only from backlog/failed)", ErrInvalidState, a.StoryID, orig.Status)
		}
		if len(a.Args.Parts) < 2 {
			return fmt.Errorf("%w: split needs at least 2 parts, got %d", ErrInvalidInput, len(a.Args.Parts))
		}
		seen := map[string]bool{}
		for i, p := range a.Args.Parts {
			pid := strings.TrimSpace(p.ID)
			if pid == "" {
				pid = fmt.Sprintf("%s-%c", a.StoryID, 'a'+i)
			}
			if pid == a.StoryID {
				return fmt.Errorf("%w: split part id %q collides with the original", ErrInvalidInput, pid)
			}
			if seen[pid] {
				return fmt.Errorf("%w: duplicate split part id %q", ErrInvalidInput, pid)
			}
			exists, err := s.storyExists(pid, orig.ProjectID)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("%w: split part id %q already exists", ErrInvalidInput, pid)
			}
			seen[pid] = true
		}
		return nil

	default:
		return fmt.Errorf("%w: unknown op %q", ErrInvalidInput, a.Op)
	}
}

// applyPlanAction executes one already-validated action via the store's team-move
// mutators (each individually transactional).
func (s *Store) applyPlanAction(projectID string, a PlanAction) error {
	switch a.Op {
	case planOpMove:
		return s.MoveStory(projectID, a.StoryID, a.Args.SprintID)

	case planOpDefer:
		st, err := s.getStory(projectID, a.StoryID)
		if err != nil {
			return err
		}
		target, needCreate, err := s.deferTarget(st)
		if err != nil {
			return err
		}
		if needCreate {
			if err := s.CreateSprint(Sprint{ID: target, Name: target, ProjectID: st.ProjectID}); err != nil && !isSQLiteConflict(err) {
				return err
			}
		}
		return s.MoveStory(projectID, a.StoryID, target)

	case planOpCancel:
		return s.CancelStory(projectID, a.StoryID)

	case planOpEdit:
		return s.EditStory(projectID, a.StoryID, StoryPatch{
			Title:     a.Args.Title,
			Body:      a.Args.Body,
			Accept:    a.Args.Acceptance,
			Owner:     a.Args.Owner,
			ScreenKey: a.Args.ScreenKey,
			Deps:      a.Args.Deps,
		})

	case planOpSplit:
		drafts := make([]StoryDraft, len(a.Args.Parts))
		for i, p := range a.Args.Parts {
			drafts[i] = StoryDraft{ID: p.ID, Title: p.Title, Body: p.Body, Accept: p.Acceptance, Owner: p.Owner}
		}
		_, err := s.SplitStory(projectID, a.StoryID, drafts)
		return err

	default:
		return fmt.Errorf("%w: unknown op %q", ErrInvalidInput, a.Op)
	}
}

var sprintSuffixRe = regexp.MustCompile(`^(.*?)(\d+)$`)

// deferTarget returns the sprint a `defer` moves st into. It is the sprint that
// fires immediately AFTER st's current sprint (sprints fire in id-ascending order);
// when st is already in the last sprint, a new one is synthesized by incrementing
// the numeric suffix of the current id (SP2 → SP3), and needCreate is true so the
// caller creates it in the same project. Only meaningful for a story with a sprint.
func (s *Store) deferTarget(st Story) (target string, needCreate bool, err error) {
	sprints, err := s.listSprints(st.ProjectID)
	if err != nil {
		return "", false, err
	}
	idx := -1
	for i, sp := range sprints {
		if sp.ID == st.SprintID {
			idx = i
			break
		}
	}
	if idx >= 0 && idx+1 < len(sprints) {
		return sprints[idx+1].ID, false, nil // the existing next sprint
	}
	// st is in the last (or an unknown) sprint — synthesize the next id.
	next := bumpSprintID(st.SprintID)
	exists, err := s.sprintExists(next, st.ProjectID)
	if err != nil {
		return "", false, err
	}
	return next, !exists, nil
}

// bumpSprintID increments the trailing number of a sprint id (SP2 → SP3); an id
// with no trailing number gets a "-2" suffix so a distinct id is always produced.
func bumpSprintID(id string) string {
	m := sprintSuffixRe.FindStringSubmatch(id)
	if m == nil {
		return id + "-2"
	}
	n, _ := strconv.Atoi(m[2])
	return m[1] + strconv.Itoa(n+1)
}

// PlanApplyRunner is the `plan_apply` step type. It reads a sprint-planning plan
// doc from the run's workdir, parses its `actions:` block, and applies it to the
// native ticket store transactionally (validate-all → apply-all → stamp planned_at).
// Like ticket_publish it holds the Store directly; it returns a FAILED StepResult
// (never an error) so a malformed plan surfaces on the run instead of panicking.
type PlanApplyRunner struct {
	Store *Store
}

// Run implements workflow.Runner. Inputs: sprint_id (required), project_id
// (optional; defaults to the default project), plan (optional doc path; defaults to
// docs/PLAN.md).
func (r *PlanApplyRunner) Run(_ context.Context, _ workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	sprintID := asString(inputs["sprint_id"])
	if sprintID == "" {
		return failResult("plan_apply: missing sprint_id"), nil
	}
	projectID := asString(inputs["project_id"])
	planPath := "docs/PLAN.md"
	if v := asString(inputs["plan"]); v != "" {
		planPath = v
	}

	raw, err := os.ReadFile(filepath.Join(workdir, planPath))
	if err != nil {
		return failResult(fmt.Sprintf("plan_apply: read %s: %v", planPath, err)), nil
	}
	actions, err := parseActions(raw)
	if err != nil {
		return failResult(fmt.Sprintf("plan_apply: parse %s: %v", planPath, err)), nil
	}
	if err := r.Store.ApplyPlan(projectID, sprintID, actions); err != nil {
		return failResult(fmt.Sprintf("plan_apply: %v", err)), nil
	}
	return workflow.StepResult{
		Success: true,
		Output:  map[string]any{"sprint_id": sprintID, "actions": len(actions)},
		Detail:  fmt.Sprintf("plan_apply: sprint=%s actions=%d applied", sprintID, len(actions)),
	}, nil
}

func failResult(detail string) workflow.StepResult {
	return workflow.StepResult{Success: false, Detail: detail}
}

// asString mirrors the agent runner's helper: renders a plumbed input as a string.
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

var yamlFenceRe = regexp.MustCompile("(?s)```(?:ya?ml)?\\s*\\n(.*?)```")

// parseActions extracts the `actions:` list from a plan doc. The planner writes the
// actions as a fenced yaml block inside the markdown doc; parseActions tries each
// fenced block (preferring one that carries an `actions:` key), then falls back to
// parsing the whole file as yaml (a planner that emitted pure yaml). A doc with no
// `actions:` key at all is an error — a plan must declare its actions explicitly
// (an intentional no-op plan writes `actions: []`).
func parseActions(raw []byte) ([]PlanAction, error) {
	for _, m := range yamlFenceRe.FindAllSubmatch(raw, -1) {
		block := m[1]
		if !containsActionsKey(block) {
			continue
		}
		var doc planDoc
		if err := yaml.Unmarshal(block, &doc); err != nil {
			return nil, fmt.Errorf("actions block: %w", err)
		}
		return doc.Actions, nil
	}
	// No fenced actions block — try the whole doc as yaml.
	if containsActionsKey(raw) {
		var doc planDoc
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		return doc.Actions, nil
	}
	return nil, fmt.Errorf("no `actions:` block found")
}

// ParseActions is the exported entry point to the SINGLE plan-actions parser (the
// plan_apply step uses the unexported parseActions; the `validate` step type reuses
// this one to lint a plan doc WITHOUT a store). There is one parser — do not add a
// second. It returns the doc's actions, or an error if the `actions:` block is absent
// or unparseable.
func ParseActions(raw []byte) ([]PlanAction, error) { return parseActions(raw) }

// IsKnownPlanOp reports whether op is one of the sprint-planning primitives. The
// `validate` step uses it to reject a plan declaring an unknown op BEFORE the doc ever
// reaches plan_apply (which rejects the same op, but only at apply time against the
// store). Keeping the op set here — next to the constants — keeps it single-sourced.
func IsKnownPlanOp(op string) bool {
	switch op {
	case planOpMove, planOpDefer, planOpCancel, planOpEdit, planOpSplit:
		return true
	}
	return false
}

// containsActionsKey reports whether a yaml chunk declares an `actions:` key at the
// start of a line (so `# actions:` in prose or an `actions` substring inside a value
// does not false-positive).
func containsActionsKey(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "actions:") {
			return true
		}
	}
	return false
}
