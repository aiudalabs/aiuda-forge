package conductor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"forge/internal/github"
	"forge/internal/tickets"
)

type fakeDispatchGH struct {
	tasks      []string // prompts de agent tasks
	models     []string
	workflows  []string // prompts vía workflow_dispatch
	wfIssues   []string // input "issues" de cada workflow_dispatch
	labeled    []int    // issues marcados agent:running al despachar (SetIssueRunning)
	copilotErr error    // si != nil, CreateAgentTask falla (para probar el fallback)
	noSecret   bool     // si true, RepoSecretExists devuelve false (claude_action sin secret)
}

func (f *fakeDispatchGH) CreateAgentTask(_ context.Context, _, prompt, model string) (string, error) {
	if f.copilotErr != nil {
		return "", f.copilotErr
	}
	f.tasks = append(f.tasks, prompt)
	f.models = append(f.models, model)
	return "https://github.com/o/r/tasks/t1", nil
}

func (f *fakeDispatchGH) DispatchWorkflow(_ context.Context, _, _, _ string, inputs map[string]string) error {
	f.workflows = append(f.workflows, inputs["prompt"])
	f.wfIssues = append(f.wfIssues, inputs["issues"])
	return nil
}

func (f *fakeDispatchGH) SetIssueRunning(_ context.Context, _ string, numbers []int) error {
	f.labeled = append(f.labeled, numbers...)
	return nil
}

func (f *fakeDispatchGH) RepoSecretExists(_ context.Context, _, _ string) (bool, error) {
	return !f.noSecret, nil
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
	// Y el conductor lo pre-marca agent:running en el t0 (cierra el hueco
	// dispatch→arranque que causaba el flap): el issue #1 quedó etiquetado.
	if len(gh.labeled) != 1 || gh.labeled[0] != 1 {
		t.Fatalf("labeled = %v, want [1]", gh.labeled)
	}
}

// TestDispatchSprintPreLabelsAllIssues: al despachar un sprint por claude_action, el
// conductor marca agent:running TODOS los issues del sprint en el t0 — no sólo el
// primero. Es la clave del fix del flap: la señal de "corriendo" existe en GitHub
// desde el dispatch, sin esperar a que el workflow arranque.
func TestDispatchSprintPreLabelsAllIssues(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st) // SP1 = S-01 (issue #1) + S-02 (issue #2)
	gh := &fakeDispatchGH{}
	d := &Dispatcher{Tickets: st, GH: gh}
	pol := Policy{ExecutionUnit: "sprint", DispatchMode: "approve", Executor: "claude_action"}

	if _, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "SP1"); err != nil {
		t.Fatal(err)
	}
	if len(gh.labeled) != 2 || gh.labeled[0] != 1 || gh.labeled[1] != 2 {
		t.Fatalf("labeled = %v, want [1 2] (todos los issues del sprint)", gh.labeled)
	}
	if gh.wfIssues[0] != "1,2" {
		t.Fatalf("issues input = %q, want \"1,2\"", gh.wfIssues[0])
	}
}

// TestDispatchCopilotDoesNotPreLabel: el canal copilot se auto-asigna el issue, así
// que NO usa el label agent:running — el conductor no debe pre-marcarlo.
func TestDispatchCopilotDoesNotPreLabel(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	gh := &fakeDispatchGH{}
	d := &Dispatcher{Tickets: st, GH: gh}
	pol := Policy{ExecutionUnit: "story", DispatchMode: "approve", Executor: "copilot"}

	if _, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "S-01"); err != nil {
		t.Fatal(err)
	}
	if len(gh.labeled) != 0 {
		t.Fatalf("labeled = %v, want [] (copilot no usa agent:running)", gh.labeled)
	}
}

// TestDispatchFallbackIsNotSilent (Bug A): si el canal pedido (copilot) FALLA, el
// dispatch cae al alterno PERO marca RequestedChannel + FallbackReason para que la UI
// avise — no se cambia de canal en silencio.
func TestDispatchFallbackIsNotSilent(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	gh := &fakeDispatchGH{copilotErr: errors.New("gh api create agent task: exit status 1: forbidden (HTTP 403)")}
	d := &Dispatcher{Tickets: st, GH: gh}
	pol := Policy{ExecutionUnit: "story", DispatchMode: "approve", Executor: "copilot"}

	res, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "S-01")
	if err != nil {
		t.Fatal(err)
	}
	if res.Channel != "claude_action" {
		t.Fatalf("channel = %q, want claude_action (fallback)", res.Channel)
	}
	if res.RequestedChannel != "copilot" {
		t.Fatalf("requested_channel = %q, want copilot", res.RequestedChannel)
	}
	if res.FallbackReason == "" || !strings.Contains(res.FallbackReason, "copilot") {
		t.Fatalf("fallback_reason vacío o sin el canal pedido: %q", res.FallbackReason)
	}
}

// TestDispatchClaudeActionBlockedWithoutSecret (Bug B): despachar claude_action sin el
// secret CLAUDE_CODE_OAUTH_TOKEN se BLOQUEA con ErrNoCapacity — no se lanza un run que
// muere al arrancar; el motivo es visible.
func TestDispatchClaudeActionBlockedWithoutSecret(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	gh := &fakeDispatchGH{noSecret: true}
	d := &Dispatcher{Tickets: st, GH: gh}
	pol := Policy{ExecutionUnit: "story", DispatchMode: "approve", Executor: "claude_action"}

	_, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "S-01")
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v, want ErrNoCapacity", err)
	}
	if !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("el motivo debería nombrar el secret: %v", err)
	}
	// No se despachó nada.
	if len(gh.workflows) != 0 {
		t.Fatalf("no debió dispararse el workflow: %d", len(gh.workflows))
	}
	if s, _ := st.GetStory("S-01"); s.Status == tickets.StatusRunning {
		t.Fatalf("S-01 no debió quedar running (dispatch bloqueado)")
	}
}

// TestDispatchClaudeActionOkWithSecret: con el secret presente, claude_action despacha
// normal (el pre-check de capacidad no bloquea).
func TestDispatchClaudeActionOkWithSecret(t *testing.T) {
	st := newStore(t)
	seedDispatch(t, st)
	gh := &fakeDispatchGH{} // noSecret=false → secret existe
	d := &Dispatcher{Tickets: st, GH: gh}
	pol := Policy{ExecutionUnit: "story", DispatchMode: "approve", Executor: "claude_action"}

	res, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "S-01")
	if err != nil {
		t.Fatal(err)
	}
	if res.Channel != "claude_action" || len(gh.workflows) != 1 {
		t.Fatalf("res=%+v workflows=%d", res, len(gh.workflows))
	}
}

// TestRecoveredStoryDispatchesOnCurrentChannel is the acceptance-criterion test:
// after the conductor recovers a story whose Copilot task was purged (404), the
// NEXT dispatch uses the project's CURRENT channel — NOT the dead task's channel
// (the live case: the project moved from copilot to claude_action). The recovered
// story carries no session/channel memory, so Candidates/Dispatch pick pol.Executor.
func TestRecoveredStoryDispatchesOnCurrentChannel(t *testing.T) {
	st := newStore(t)
	if err := st.CreateStory(tickets.Story{ID: "S-01", Title: "Auth", Body: "b", Accept: "- a", Owner: "python-dev", ProjectID: "p1", ExternalRef: "github:o/r#1"}); err != nil {
		t.Fatal(err)
	}
	// Dispatched to copilot: running + a task session that GitHub later purges.
	dispatchTaskSession(t, st, "S-01", "https://github.com/o/r/tasks/dead-1234abcd")

	// The projection declares the purged task dead → S-01 returns to backlog.
	gh := &fakeGH{issues: []github.IssueState{{Number: 1, State: "open"}}}
	p := NewProjector(st, gh)
	p.TaskState = &errTaskState{err: fmt.Errorf("gh api agent task: %w: not found", github.ErrTaskNotFound)}
	for i := 0; i < notFoundDeathThreshold; i++ {
		if _, err := p.SyncProject(context.Background(), "p1", "https://github.com/o/r"); err != nil {
			t.Fatal(err)
		}
	}
	if s, _ := st.GetStory("S-01"); s.Status != tickets.StatusBacklog {
		t.Fatalf("precondición: S-01 = %s, want backlog (task muerta recuperada)", s.Status)
	}

	// The project's CURRENT channel is now claude_action (changed from the dead
	// task's copilot). The re-dispatch must use it.
	fd := &fakeDispatchGH{}
	d := &Dispatcher{Tickets: st, GH: fd}
	pol := Policy{ExecutionUnit: "story", DispatchMode: "approve", Executor: "claude_action"}
	res, err := d.Dispatch(context.Background(), "p1", "https://github.com/o/r", pol, "S-01")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.Channel != "claude_action" {
		t.Fatalf("re-dispatch channel = %q, want claude_action (canal VIGENTE, no el de la task muerta)", res.Channel)
	}
	if len(fd.workflows) != 1 || len(fd.tasks) != 0 {
		t.Fatalf("debió disparar el workflow claude_action, no una task copilot (wf=%d task=%d)", len(fd.workflows), len(fd.tasks))
	}
	// Re-dispatch clears the "agente perdido" badge.
	if s, _ := st.GetStory("S-01"); s.AgentLost != "" {
		t.Fatalf("el re-despacho debió limpiar agent_lost, got %q", s.AgentLost)
	}
}
