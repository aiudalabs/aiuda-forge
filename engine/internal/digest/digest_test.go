package digest

import (
	"context"
	"strings"
	"testing"
	"time"

	"forge/internal/store"
	"forge/internal/tickets"
)

// fakeStories / fakeRuns are in-memory sources so the digest logic is tested
// without a database.
type fakeStories struct{ stories []tickets.Story }

func (f fakeStories) ListStoriesByProject(string) ([]tickets.Story, error) { return f.stories, nil }

type fakeRuns struct {
	runs  []*store.Run
	tasks map[string][]*store.Task
}

func (f fakeRuns) ListRunsByProject(_ store.Status, _ string) ([]*store.Run, error) {
	return f.runs, nil
}
func (f fakeRuns) TasksForRun(runID string) ([]*store.Task, error) { return f.tasks[runID], nil }

// The four section headers the standup must always carry.
var sections = []string{"📈 Qué avanzó", "🚧 Qué bloquea", "⏭️ Qué sigue", "💰 Cuánto costó"}

func assertFourSections(t *testing.T, text string) {
	t.Helper()
	for _, s := range sections {
		if !strings.Contains(text, s) {
			t.Errorf("digest missing section %q\n---\n%s", s, text)
		}
	}
}

func TestDigestFourSectionsWithData(t *testing.T) {
	stories := []tickets.Story{
		{ID: "S1", Title: "login", Owner: "react-dev", SprintID: "SP1", Status: tickets.StatusDone, RunID: "r1"},
		{ID: "S2", Title: "api", Owner: "python-dev", SprintID: "SP1", Status: tickets.StatusRunning, RunID: "r2"},
		{ID: "S3", Title: "reports", Owner: "python-dev", SprintID: "SP2", Status: tickets.StatusFailed, RunID: "r3"},
		// S4 is in the backlog and depends on S3 (not done) → a blocker waiting on SP2.
		{ID: "S4", Title: "export", Owner: "python-dev", SprintID: "SP3", Status: tickets.StatusBacklog, Deps: []string{"S3"}},
	}
	runs := []*store.Run{
		{ID: "r1", WorkflowID: "factory", UpdatedAt: 1000},
		{ID: "r2", WorkflowID: "factory", UpdatedAt: 1000},
		{ID: "r3", WorkflowID: "factory", UpdatedAt: 1000},
	}
	tasks := map[string][]*store.Task{
		"r3": {{StepID: "implement", Status: store.StatusFailed, Error: "agent error: idle timeout after 8m"}},
	}
	d := &Digester{
		Stories: fakeStories{stories},
		Runs:    fakeRuns{runs: runs, tasks: tasks},
		Spend:   func(string, int64) (float64, error) { return 4.25, nil },
		Now:     func() time.Time { return time.UnixMilli(2000).UTC() },
	}

	data, err := d.Gather("proj", "MarketPTY", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Done) != 1 || data.Done[0].ID != "S1" {
		t.Fatalf("Done = %+v, want [S1]", data.Done)
	}
	if len(data.Running) != 1 || data.Running[0].ID != "S2" {
		t.Fatalf("Running = %+v, want [S2]", data.Running)
	}
	if len(data.Failed) != 1 || data.Failed[0].ID != "S3" {
		t.Fatalf("Failed = %+v, want [S3]", data.Failed)
	}
	if len(data.Blockers) != 1 || data.Blockers[0].Story.ID != "S4" {
		t.Fatalf("Blockers = %+v, want [S4]", data.Blockers)
	}
	if got := data.Blockers[0].OnSprints; len(got) != 1 || got[0] != "SP2" {
		t.Fatalf("blocker OnSprints = %v, want [SP2]", got)
	}
	if len(data.ProblemRuns) != 1 || !strings.Contains(data.ProblemRuns[0].Reason, "timed out") {
		t.Fatalf("ProblemRuns = %+v, want a timeout", data.ProblemRuns)
	}
	if data.SpendUSD != 4.25 {
		t.Fatalf("SpendUSD = %v, want 4.25", data.SpendUSD)
	}

	// The deterministic render (no LLM) carries all four sections + the real content.
	text, err := d.Build(context.Background(), "proj", "MarketPTY", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	assertFourSections(t, text)
	for _, want := range []string{"S1", "S2", "S3", "esperando a SP2", "$4.25"} {
		if !strings.Contains(text, want) {
			t.Errorf("digest missing %q\n---\n%s", want, text)
		}
	}
}

func TestDigestEmptyStillFourSections(t *testing.T) {
	d := &Digester{Stories: fakeStories{}, Runs: fakeRuns{}}
	text, err := d.Build(context.Background(), "proj", "Empty", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	assertFourSections(t, text)
}

func TestDigestWindowFiltersByRun(t *testing.T) {
	// A done story whose run last changed BEFORE the window is excluded.
	stories := []tickets.Story{
		{ID: "OLD", Title: "old", Status: tickets.StatusDone, RunID: "rold"},
		{ID: "NEW", Title: "new", Status: tickets.StatusDone, RunID: "rnew"},
	}
	runs := []*store.Run{
		{ID: "rold", UpdatedAt: 500},
		{ID: "rnew", UpdatedAt: 1500},
	}
	d := &Digester{Stories: fakeStories{stories}, Runs: fakeRuns{runs: runs}}
	data, err := d.Gather("proj", "p", time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Done) != 1 || data.Done[0].ID != "NEW" {
		t.Fatalf("windowed Done = %+v, want [NEW]", data.Done)
	}
}

func TestDigestUsesSynthesizer(t *testing.T) {
	var gotSystem, gotUser string
	d := &Digester{
		Stories: fakeStories{},
		Runs:    fakeRuns{},
		Synthesize: func(_ context.Context, system, user string) (string, error) {
			gotSystem, gotUser = system, user
			return "LLM STANDUP", nil
		},
	}
	text, err := d.Build(context.Background(), "proj", "p", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if text != "LLM STANDUP" {
		t.Fatalf("Build = %q, want the synthesizer output", text)
	}
	if !strings.Contains(gotSystem, "standup") {
		t.Errorf("synthesizer system prompt missing standup framing: %q", gotSystem)
	}
	if !strings.Contains(gotUser, "Qué avanzó") {
		t.Errorf("synthesizer user prompt should carry the deterministic facts, got %q", gotUser)
	}
}

func TestDigestSynthesizerErrorFallsBack(t *testing.T) {
	d := &Digester{
		Stories:    fakeStories{},
		Runs:       fakeRuns{},
		Synthesize: func(context.Context, string, string) (string, error) { return "", context.DeadlineExceeded },
	}
	text, err := d.Build(context.Background(), "proj", "p", time.Time{})
	if err != nil {
		t.Fatalf("Build should not error on synth failure: %v", err)
	}
	assertFourSections(t, text) // fell back to the deterministic render
}
