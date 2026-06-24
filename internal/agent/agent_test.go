package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vibeforge-kernel/internal/store"
	"vibeforge-kernel/internal/workflow"
)

// recordingBackend captures the last Options it was called with (to assert
// cross-model + tool wiring) and delegates behavior to an embedded FakeBackend.
type recordingBackend struct {
	FakeBackend
	lastOpts   Options
	lastPrompt string
}

func (r *recordingBackend) Run(ctx context.Context, prompt string, opts Options, onEvent func(Event)) (Result, error) {
	r.lastOpts = opts
	r.lastPrompt = prompt
	return r.FakeBackend.Run(ctx, prompt, opts, onEvent)
}

func newEngineWithAgent(t *testing.T, backend Backend, agents Loader) *workflow.Engine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := workflow.NewEngine(st, nil, t.TempDir())
	e.Register("echo", workflow.EchoRunner{})
	e.Register("gate", workflow.GateRunner{})
	e.Register("agent", NewStepRunnerWith(backend, agents))
	return e
}

// NewStepRunnerWith is a tiny test ctor with no timeout (fake is instant).
func NewStepRunnerWith(backend Backend, agents Loader) *StepRunner {
	r := NewStepRunner(backend, agents)
	r.Timeout = 0
	return r
}

// TestAgentStepE2E: an `agent` step (fake backend that stages a file) feeding a
// gate, run end-to-end through the generic engine — proving the agent step type
// plugs into the data-defined flow.
func TestAgentStepE2E(t *testing.T) {
	wfSrc := []byte(`
id: build
version: 1.0.0
steps:
  - id: implement
    type: agent
    agent: dev
    inputs: { ticket: $trigger.ticket }
  - id: gate
    type: gate
    command: "test -f impl.txt"
`)
	wf, err := workflow.Parse(wfSrc)
	if err != nil {
		t.Fatal(err)
	}
	agents := MapLoader{"dev": &Manifest{ID: "dev", Model: "claude-opus-4-8", Tools: []string{"read", "edit", "write", "bash"}, Role: "impl", Persona: "be precise"}}
	backend := FakeBackend{Reply: "done", Script: func(workdir, prompt string) error {
		return os.WriteFile(filepath.Join(workdir, "impl.txt"), []byte("ok"), 0o644)
	}}

	e := newEngineWithAgent(t, backend, agents)
	e.Loader = workflow.MapLoader{"build": wf}

	runID, err := e.StartRun("build", map[string]any{"ticket": "add a thing"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := e.RunToCompletion(context.Background(), runID)
	if err != nil || status != store.StatusDone {
		t.Fatalf("expected DONE, got %s err=%v", status, err)
	}
}

// TestCrossModelOverride: a step.Model in the workflow overrides the manifest
// model. This is the cross-model mechanism (reviewer != implementer model).
func TestCrossModelOverride(t *testing.T) {
	agents := MapLoader{"reviewer": &Manifest{ID: "reviewer", Model: "manifest-default-model", Tools: []string{"read"}}}
	rec := &recordingBackend{}
	runner := NewStepRunnerWith(rec, agents)

	step := workflow.Step{ID: "review", Type: "agent", Agent: "reviewer", Model: "claude-sonnet-4-6", Prompt: "adversarial"}
	_, err := runner.Run(context.Background(), step, map[string]any{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rec.lastOpts.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected step model override claude-sonnet-4-6, got %q", rec.lastOpts.Model)
	}
}

// TestManifestModelFallback: with no step.Model, the manifest model is used.
func TestManifestModelFallback(t *testing.T) {
	agents := MapLoader{"dev": &Manifest{ID: "dev", Model: "claude-opus-4-8", Tools: []string{"read", "edit"}}}
	rec := &recordingBackend{}
	runner := NewStepRunnerWith(rec, agents)
	step := workflow.Step{ID: "implement", Type: "agent", Agent: "dev"}
	if _, err := runner.Run(context.Background(), step, nil, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if rec.lastOpts.Model != "claude-opus-4-8" {
		t.Fatalf("expected manifest model, got %q", rec.lastOpts.Model)
	}
	// Tools mapped to claude names.
	want := map[string]bool{"Read": true, "Edit": true}
	for _, tool := range rec.lastOpts.AllowedTools {
		delete(want, tool)
	}
	if len(want) != 0 {
		t.Fatalf("expected Read+Edit mapped, missing %v (got %v)", want, rec.lastOpts.AllowedTools)
	}
}

// TestAgentFailurePropagates: a failing agent (is_error) makes the step fail.
func TestAgentFailurePropagates(t *testing.T) {
	agents := MapLoader{"dev": &Manifest{ID: "dev"}}
	runner := NewStepRunnerWith(FakeBackend{Fail: true}, agents)
	res, err := runner.Run(context.Background(), workflow.Step{Type: "agent", Agent: "dev"}, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatalf("expected step failure when agent reports is_error")
	}
}

// TestParseStreamLine: the NDJSON parser extracts text, tool_use, and result
// from claude stream-json lines (the validated v1 format).
func TestParseStreamLine(t *testing.T) {
	assistant := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "tool_use", "name": "Edit"},
			},
		},
	}
	ev, _, isResult := parseStreamLine(assistant)
	if isResult {
		t.Fatal("assistant line is not a result")
	}
	var sawText, sawTool bool
	for _, e := range ev {
		if e.Kind == KindText && e.Text == "hello" {
			sawText = true
		}
		if e.Kind == KindToolUse && e.Tool == "Edit" {
			sawTool = true
		}
	}
	if !sawText || !sawTool {
		t.Fatalf("expected text+tool_use events, got %+v", ev)
	}

	resultLine := map[string]any{
		"type": "result", "result": "all done", "is_error": false,
		"total_cost_usd": 0.0123, "num_turns": float64(4),
	}
	_, res, isResult := parseStreamLine(resultLine)
	if !isResult {
		t.Fatal("expected result line")
	}
	if !res.Success || res.Text != "all done" || res.NumTurns != 4 || res.CostUSD != 0.0123 {
		t.Fatalf("bad result parse: %+v", res)
	}
}

// TestChildEnvAuth: each auth mode sets exactly the right token var.
func TestChildEnvAuth(t *testing.T) {
	env := childEnv(Auth{Mode: AuthAPIKey, Token: "sk-test"})
	if !hasEnv(env, "ANTHROPIC_API_KEY=sk-test") {
		t.Fatal("api_key mode should set ANTHROPIC_API_KEY")
	}
	if hasEnvKey(env, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatal("api_key mode should clear oauth token")
	}

	env = childEnv(Auth{Mode: AuthOAuthToken, Token: "oauth-xyz"})
	if !hasEnv(env, "CLAUDE_CODE_OAUTH_TOKEN=oauth-xyz") {
		t.Fatal("oauth mode should set CLAUDE_CODE_OAUTH_TOKEN")
	}
	if hasEnvKey(env, "ANTHROPIC_API_KEY") {
		t.Fatal("oauth mode should clear api key")
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
func hasEnvKey(env []string, key string) bool {
	for _, e := range env {
		if len(e) > len(key) && e[:len(key)+1] == key+"=" {
			return true
		}
	}
	return false
}

// TestDirLoaderLoadsPersona: the registry agents on disk load with persona.
func TestDirLoaderLoadsPersona(t *testing.T) {
	loader := NewDirLoader(filepath.Join("..", "..", "registry", "agents"))
	dev, err := loader.Load("dev")
	if err != nil {
		t.Fatal(err)
	}
	if dev.Model == "" || dev.Persona == "" {
		t.Fatalf("expected dev manifest with model+persona, got %+v", dev)
	}
	rev, err := loader.Load("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if rev.Model == dev.Model {
		t.Fatalf("reviewer should use a different model than dev (cross-model), both %q", rev.Model)
	}
}

// TestDesignWorkflowParses: the design.yaml workflow loads and parses with the kernel parser.
func TestDesignWorkflowParses(t *testing.T) {
	wf, err := workflow.ParseFile(filepath.Join("..", "..", "registry", "workflows", "design.yaml"))
	if err != nil {
		t.Fatalf("design.yaml failed to parse: %v", err)
	}
	if wf.ID != "design" {
		t.Fatalf("expected workflow id 'design', got %q", wf.ID)
	}
	// Verify all five design phases and their gates are present.
	phases := []string{"discovery", "discovery_gate", "prd", "prd_gate", "architecture", "arch_gate", "ui", "ui_gate", "backlog", "backlog_gate"}
	for _, id := range phases {
		if _, ok := wf.StepByID(id); !ok {
			t.Errorf("design.yaml missing step %q", id)
		}
	}
}

// TestDesignStepOutputWritesDoc: a design step with inputs["output"] set writes the
// agent's result text to the declared path under the workdir.
func TestDesignStepOutputWritesDoc(t *testing.T) {
	workdir := t.TempDir()
	resultText := "# Project Brief\n\nThis is the brief.\n"

	agents := MapLoader{
		"analyst": &Manifest{ID: "analyst", Model: "claude-sonnet-4-6", Tools: []string{"read", "write"}, Role: "analyst"},
	}
	backend := FakeBackend{Reply: resultText}

	runner := NewStepRunnerWith(backend, agents)
	runner.Sandboxed = false

	step := workflow.Step{ID: "discovery", Type: "design", Agent: "analyst"}
	inputs := map[string]any{
		"instructions": "Build a task manager for developers.",
		"output":       "docs/BRIEF.md",
	}

	res, err := runner.Run(context.Background(), step, inputs, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("expected success, got detail: %s", res.Detail)
	}

	// Verify the doc was written to disk at the declared relative path.
	docPath := filepath.Join(workdir, "docs", "BRIEF.md")
	got, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("output doc not written to %s: %v", docPath, err)
	}
	if string(got) != resultText {
		t.Fatalf("output doc content mismatch\nwant: %q\ngot:  %q", resultText, string(got))
	}

	// Verify the output path is included in the step result.
	outVal, ok := res.Output["output"]
	if !ok {
		t.Fatal("step result output map missing 'output' key")
	}
	if outVal != docPath {
		t.Fatalf("expected output path %q, got %q", docPath, outVal)
	}
}
