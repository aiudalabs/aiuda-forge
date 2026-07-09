package validate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/method"
	"forge/internal/tickets"
	"forge/internal/workflow"
)

// agentsDir writes a minimal registry agents dir with the given lane ids so the backlog
// owner check has a known-lane set, and returns its path.
func agentsDir(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, id := range ids {
		if err := os.WriteFile(filepath.Join(dir, id+".yaml"), []byte("id: "+id+"\nmodel: claude\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ── prd ──────────────────────────────────────────────────────────────────────────

const prdValid = `## Product Requirements Document
### 3. Functional Requirements
#### Epic 1 — Auth
- **FR-01 [P0]**: The system MUST let a user log in.
  - Scenario: happy path
    - GIVEN a registered user
    - WHEN they submit valid credentials
    - THEN a session is created
### 4. Non-Functional Requirements
- NFR-01 [Security]: passwords hashed.
`

// prdBroken: FR-02 has NO scenario (the section 4 heading bounds its scope so it cannot
// borrow FR-01's GWT).
const prdBroken = `## Product Requirements Document
### 3. Functional Requirements
#### Epic 1 — Auth
- **FR-01 [P0]**: The system MUST let a user log in.
  - Scenario: happy path
    - GIVEN a registered user
    - WHEN they submit valid credentials
    - THEN a session is created
- **FR-02 [P1]**: The system MUST let a user log out.
### 4. Non-Functional Requirements
- NFR-01 [Security]: passwords hashed.
`

func TestCheckPRD(t *testing.T) {
	if err := checkPRD([]byte(prdValid)); err != nil {
		t.Fatalf("valid PRD rejected: %v", err)
	}
	err := checkPRD([]byte(prdBroken))
	if err == nil {
		t.Fatal("PRD with a scenario-less FR-02 should fail")
	}
	if !strings.Contains(err.Error(), "FR-02") {
		t.Errorf("detail must name FR-02, got: %v", err)
	}
	// A doc with no requirements at all is malformed.
	if err := checkPRD([]byte("# PRD\nSome prose, no requirements.\n")); err == nil {
		t.Error("PRD with no FR-NN should fail")
	}
}

// ── backlog ──────────────────────────────────────────────────────────────────────

const backlogValid = `epic: {id: E1, title: Auth}
sprints:
  - {id: SP1, name: Sprint 1}
stories:
  - id: S1
    title: Login screen
    owner: react-dev
    sprint_id: SP1
    screen_key: customer.login
  - id: S2
    title: Auth API
    owner: python-dev
    sprint_id: SP1
    deps: [S1]
`

func TestCheckBacklog(t *testing.T) {
	r := &Runner{AgentsDir: agentsDir(t, "react-dev", "python-dev", "flutter-dev", "dev")}

	if err := r.checkBacklog([]byte(backlogValid)); err != nil {
		t.Fatalf("valid backlog rejected: %v", err)
	}

	cases := []struct {
		name, doc, want string
	}{
		{
			name: "dep to missing id",
			doc: `stories:
  - {id: S1, title: A, owner: python-dev}
  - {id: S2, title: B, owner: python-dev, deps: [S9]}
`,
			want: "S9",
		},
		{
			name: "duplicate id",
			doc: `stories:
  - {id: S1, title: A, owner: python-dev}
  - {id: S1, title: B, owner: python-dev}
`,
			want: "duplicate story id",
		},
		{
			name: "unknown owner",
			doc: `stories:
  - {id: S1, title: A, owner: wizard-dev}
`,
			want: "not a known lane",
		},
		{
			name: "frontend story without screen_key",
			doc: `stories:
  - {id: S1, title: Login, owner: react-dev}
`,
			want: "screen_key",
		},
		{
			name: "depends_on alias to missing id",
			doc: `stories:
  - {id: S1, title: A, owner: python-dev}
  - {id: S2, title: B, owner: python-dev, depends_on: [ghost]}
`,
			want: "ghost",
		},
		{
			name: "not yaml",
			doc:  "this: [is: not: valid: yaml",
			want: "does not parse",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.checkBacklog([]byte(tc.doc))
			if err == nil {
				t.Fatalf("expected failure for %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("detail must contain %q, got: %v", tc.want, err)
			}
		})
	}

	// With no agents dir the lane check is skipped: an unknown owner passes (the other
	// rules still apply) — graceful degradation, not a false-fail on every backlog.
	rNoLanes := &Runner{}
	if err := rNoLanes.checkBacklog([]byte(`stories:
  - {id: S1, title: A, owner: whoever}
`)); err != nil {
		t.Errorf("with no lane set, unknown owner must not fail: %v", err)
	}
}

// ── provisioning ───────────────────────────────────────────────────────────────

const provisioningValid = `version: 1
stack: react-supabase
roles: []
indexes:
  derive: true
  required: []
dependencies: []
services: []
authz: []
bootstrap: []
`

func TestCheckProvisioning(t *testing.T) {
	if err := checkProvisioning([]byte(provisioningValid)); err != nil {
		t.Fatalf("valid provisioning rejected: %v", err)
	}
	// Missing a contract key (authz) fails, naming it.
	missing := strings.Replace(provisioningValid, "authz: []\n", "", 1)
	err := checkProvisioning([]byte(missing))
	if err == nil || !strings.Contains(err.Error(), "authz") {
		t.Errorf("missing authz key must fail naming it, got: %v", err)
	}
	// Wrong shape: roles as a scalar.
	if err := checkProvisioning([]byte(strings.Replace(provisioningValid, "roles: []", "roles: nope", 1))); err == nil || !strings.Contains(err.Error(), "roles") {
		t.Errorf("roles as scalar must fail, got: %v", err)
	}
	if err := checkProvisioning([]byte("just a string")); err == nil {
		t.Error("non-mapping provisioning must fail")
	}
}

// ── plan (shared parser) ─────────────────────────────────────────────────────────

func TestCheckPlan(t *testing.T) {
	valid := `actions:
  - op: move
    story_id: S1
    args:
      sprint_id: SP2
`
	if err := checkPlan([]byte(valid)); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	unknownOp := `actions:
  - op: frobnicate
    story_id: S1
`
	if err := checkPlan([]byte(unknownOp)); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("unknown op must fail, got: %v", err)
	}
	noStory := `actions:
  - op: cancel
`
	if err := checkPlan([]byte(noStory)); err == nil || !strings.Contains(err.Error(), "story_id") {
		t.Errorf("missing story_id must fail, got: %v", err)
	}
	if err := checkPlan([]byte("# a plan with no actions block")); err == nil {
		t.Error("plan with no actions block must fail")
	}
}

// TestPlanParserIsShared proves the validate step and plan_apply use the SAME parser
// and op set — no divergent copy. tickets.ParseActions is the exported entry to the
// unexported parseActions plan_apply calls; IsKnownPlanOp is single-sourced next to the
// op constants.
func TestPlanParserIsShared(t *testing.T) {
	actions, err := tickets.ParseActions([]byte("actions:\n  - op: split\n    story_id: S1\n"))
	if err != nil || len(actions) != 1 || actions[0].Op != "split" {
		t.Fatalf("ParseActions: %v %+v", err, actions)
	}
	for _, op := range []string{"move", "defer", "cancel", "edit", "split"} {
		if !tickets.IsKnownPlanOp(op) {
			t.Errorf("IsKnownPlanOp(%q) = false, want true", op)
		}
	}
	if tickets.IsKnownPlanOp("nope") {
		t.Error("IsKnownPlanOp(nope) = true, want false")
	}
}

// ── retro (shared parser + guardrails) ───────────────────────────────────────────

func TestCheckRetro(t *testing.T) {
	valid := `proposals:
  - kind: skill
    id: frontend-quality
    content: |
      # updated skill
`
	if err := checkRetro([]byte(valid)); err != nil {
		t.Fatalf("valid retro rejected: %v", err)
	}
	badKind := `proposals:
  - kind: banana
    id: x
    content: hi
`
	if err := checkRetro([]byte(badKind)); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Errorf("illegal kind must fail, got: %v", err)
	}
	// Protected: the retro's own method is not self-editable.
	protectedDoc := `proposals:
  - kind: agent
    id: retro-analyst
    content: |
      id: retro-analyst
      model: claude
`
	if err := checkRetro([]byte(protectedDoc)); err == nil || !strings.Contains(err.Error(), "retro-analyst") {
		t.Errorf("protected method edit must fail, got: %v", err)
	}
	// Empty proposals block is a legal no-op retro.
	if err := checkRetro([]byte("# retro with no proposals\n")); err != nil {
		t.Errorf("empty retro must pass: %v", err)
	}
}

// TestRetroGuardrailsAreShared proves the validate step reuses registry_apply's exact
// guardrail check (method.ValidateProposal) — one rule, no drift.
func TestRetroGuardrailsAreShared(t *testing.T) {
	if err := method.ValidateProposal(method.Proposal{Kind: "workflow", ID: "retro", Content: "id: x"}); err == nil {
		t.Error("ValidateProposal must reject the protected retro workflow")
	}
	if err := method.ValidateProposal(method.Proposal{Kind: "skill", ID: "ok", Content: "# fine"}); err != nil {
		t.Errorf("a legal skill proposal must pass: %v", err)
	}
}

// ── Run dispatch + file IO ───────────────────────────────────────────────────────

func TestRun(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "PRD.md"), []byte(prdValid), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{}

	res, _ := r.Run(context.Background(), workflow.Step{}, map[string]any{"schema": "prd", "path": "PRD.md"}, work)
	if !res.Success {
		t.Fatalf("valid PRD via Run should succeed, detail=%s", res.Detail)
	}

	// A malformed doc returns a FAILED result (never an error), with the schema + path
	// in the detail so the on_fail loop has something actionable.
	if err := os.WriteFile(filepath.Join(work, "bad.md"), []byte("no requirements"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, runErr := r.Run(context.Background(), workflow.Step{}, map[string]any{"schema": "prd", "path": "bad.md"}, work)
	if runErr != nil {
		t.Fatalf("Run must not return an error, got %v", runErr)
	}
	if res.Success {
		t.Fatal("malformed PRD via Run should fail")
	}
	if !strings.Contains(res.Detail, "prd") || !strings.Contains(res.Detail, "bad.md") {
		t.Errorf("detail must name schema+path, got: %s", res.Detail)
	}

	// Missing schema / path / file each fail cleanly.
	for _, in := range []map[string]any{
		{"path": "PRD.md"},                      // no schema
		{"schema": "prd"},                       // no path
		{"schema": "prd", "path": "nope.md"},    // missing file
		{"schema": "mystery", "path": "PRD.md"}, // unknown schema
	} {
		res, err := r.Run(context.Background(), workflow.Step{}, in, work)
		if err != nil {
			t.Fatalf("Run(%v) returned error: %v", in, err)
		}
		if res.Success {
			t.Errorf("Run(%v) should fail", in)
		}
	}
}
