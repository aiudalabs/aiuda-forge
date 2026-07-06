package conductor

import (
	"context"
	"strings"
	"testing"

	"forge/internal/tickets"
)

type fakeDispatchGH struct {
	tasks     []string // prompts de agent tasks
	models    []string
	workflows []string // prompts vía workflow_dispatch
	wfIssues  []string // input "issues" de cada workflow_dispatch
}

func (f *fakeDispatchGH) CreateAgentTask(_ context.Context, _, prompt, model string) (string, error) {
	f.tasks = append(f.tasks, prompt)
	f.models = append(f.models, model)
	return "https://github.com/o/r/tasks/t1", nil
}

func (f *fakeDispatchGH) DispatchWorkflow(_ context.Context, _, _, _ string, inputs map[string]string) error {
	f.workflows = append(f.workflows, inputs["prompt"])
	f.wfIssues = append(f.wfIssues, inputs["issues"])
	return nil
}

// seedDispatch: SP1 con dos stories listas; SP2 con una story gateada por SP1.
func seedDispatch(t *testing.T, st *tickets.Store) {
	t.Helper()
	stories := []tickets.Story{
		{ID: "S-01", Title: "Schema", Body: "as an op", Accept: "- a\n- b", SprintID: "SP1", Owner: "python-dev", ProjectID: "p1", ExternalRef: "github:o/r#1"},
		{ID: "S-02", Title: "Auth", SprintID: "SP1", Owner: "python-dev", ProjectID: "p1", ExternalRef: "github:o/r#2"},
		{ID: "S-03", Title: "Login UI", SprintID: "SP2", Owner: "react-dev", ProjectID: "p1", Deps: []string{"S-01"}, ExternalRef: "github:o/r#3"},
	}
	for _, s := range stories {
		if err := st.CreateStory(s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}
}

func TestCandidatesSprintMode(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	d := &Dispatcher{Tickets: st, GH: &fakeDispatchGH{}}
	pol := Policy{ExecutionUnit: "sprint", DispatchMode: "approve", Executor: "copilot",
		ModelByLane: map[string]string{"python-dev": "claude-sonnet-4.6"}}

	cands, err := d.Candidates(context.Background(), "p1", "https://github.com/o/r", pol)
	if err != nil {
		t.Fatal(err)
	}
	// Solo SP1: SP2 está gateado por S-01 (cross-sprint, no done).
	if len(cands) != 1 || cands[0].ID != "SP1" || cands[0].Kind != "sprint" {
		t.Fatalf("candidates = %+v, want solo SP1", cands)
	}
	if got := cands[0].Stories; len(got) != 2 || got[0] != "S-01" || got[1] != "S-02" {
		t.Fatalf("SP1 stories = %v", got)
	}
	if cands[0].Model != "claude-sonnet-4.6" || cands[0].Lane != "python-dev" {
		t.Fatalf("lane/model = %s/%s", cands[0].Lane, cands[0].Model)
	}
}

func TestCandidatesStoryModeAndGating(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	d := &Dispatcher{Tickets: st, GH: &fakeDispatchGH{}}
	pol := Policy{ExecutionUnit: "story", DispatchMode: "approve", Executor: "copilot"}

	cands, err := d.Candidates(context.Background(), "p1", "https://github.com/o/r", pol)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 { // S-01 y S-02; S-03 espera a S-01
		t.Fatalf("candidates = %+v, want S-01+S-02", cands)
	}
	// off apaga todo
	if cs, _ := d.Candidates(context.Background(), "p1", "https://github.com/o/r", Policy{DispatchMode: "off"}); cs != nil {
		t.Fatalf("off debería dar 0 candidatos, dio %v", cs)
	}
}

func TestDispatchSprintFiresOneTaskAndMarksRunning(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	gh := &fakeDispatchGH{}
	d := &Dispatcher{Tickets: st, GH: gh}
	pol := Policy{ExecutionUnit: "sprint", DispatchMode: "approve", Executor: "copilot",
		ModelByLane: map[string]string{"python-dev": "claude-sonnet-4.6"}}

	res, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "SP1")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dispatched) != 2 || res.Channel != "copilot" || res.TaskURL == "" {
		t.Fatalf("res = %+v", res)
	}
	if len(gh.tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (goal mode: UN task por sprint)", len(gh.tasks))
	}
	p := gh.tasks[0]
	for _, want := range []string{"ONE pull request", "S-01 — Schema (issue #1)", "Closes #1, Closes #2", "Acceptance criteria:"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt sin %q:\n%s", want, p)
		}
	}
	if gh.models[0] != "claude-sonnet-4.6" {
		t.Fatalf("model = %q", gh.models[0])
	}
	for _, id := range []string{"S-01", "S-02"} {
		if s, _ := st.GetStory(id); s.Status != tickets.StatusRunning {
			t.Fatalf("%s = %s, want running", id, s.Status)
		}
	}
	// Ya en vuelo → deja de ser candidato → 409 semántico.
	if _, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "SP1"); err == nil || !strings.Contains(err.Error(), "not a dispatchable") {
		t.Fatalf("re-dispatch debería fallar con ErrNotCandidate, err=%v", err)
	}
}

func TestDispatchStoryViaClaudeAction(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	gh := &fakeDispatchGH{}
	d := &Dispatcher{Tickets: st, GH: gh}
	pol := Policy{ExecutionUnit: "story", DispatchMode: "approve", Executor: "claude_action"}

	res, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "S-01")
	if err != nil {
		t.Fatal(err)
	}
	if res.Channel != "claude_action" || len(gh.workflows) != 1 || len(gh.tasks) != 0 {
		t.Fatalf("res=%+v workflows=%d tasks=%d", res, len(gh.workflows), len(gh.tasks))
	}
	if !strings.Contains(gh.workflows[0], "Resolve issue #1") || !strings.Contains(gh.workflows[0], "Closes #1") {
		t.Fatalf("prompt = %s", gh.workflows[0])
	}
	// El nº de issue viaja como input para que el workflow lo marque agent:running.
	if gh.wfIssues[0] != "1" {
		t.Fatalf("issues input = %q, want \"1\"", gh.wfIssues[0])
	}
}
