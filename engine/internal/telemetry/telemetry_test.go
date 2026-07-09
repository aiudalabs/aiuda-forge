package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"forge/internal/store"
	"forge/internal/tickets"
)

// fakeRuns is an in-memory RunSource.
type fakeRuns struct {
	runs   []*store.Run
	tasks  map[string][]*store.Task
	events map[string][]*store.Event
}

func (f *fakeRuns) ListRunsByProject(_ store.Status, _ string) ([]*store.Run, error) {
	return f.runs, nil
}
func (f *fakeRuns) TasksForRun(runID string) ([]*store.Task, error) { return f.tasks[runID], nil }
func (f *fakeRuns) EventsAfter(runID string, _ int64) ([]*store.Event, error) {
	return f.events[runID], nil
}

type fakeStories struct{ stories []tickets.Story }

func (f *fakeStories) StoriesBySprintScoped(_, _ string) ([]tickets.Story, error) {
	return f.stories, nil
}

func payload(sprintID, extra string) string {
	if extra != "" {
		return fmt.Sprintf(`{"sprint_id":%q,%s}`, sprintID, extra)
	}
	return fmt.Sprintf(`{"sprint_id":%q}`, sprintID)
}

// TestSprintTelemetryAllFiveCategories aggregates the five categories from fake data:
// (1) per-step run telemetry (retries/stalls/durations), (2) gate decisions with text,
// (3) plan actions, (4) review decision, (5) story outcomes — plus spend.
func TestSprintTelemetryAllFiveCategories(t *testing.T) {
	runs := &fakeRuns{
		runs: []*store.Run{
			{ID: "run-plan", WorkflowID: "sprint-planning", Status: "DONE", Payload: payload("SP1", "")},
			{ID: "run-fac", WorkflowID: "factory", Status: "DONE", Payload: payload("SP1", "")},
			{ID: "run-rev", WorkflowID: "sprint-review", Status: "DONE", Payload: payload("SP1", "")},
			{ID: "run-other", WorkflowID: "factory", Status: "DONE", Payload: payload("SP9", "")}, // different sprint — excluded
		},
		tasks: map[string][]*store.Task{
			"run-plan": {
				{ID: "t-plan", RunID: "run-plan", StepID: "apply", Type: "plan_apply", Status: "DONE",
					Result: `{"success":true,"output":{"sprint_id":"SP1","actions":3},"detail":"plan_apply: 3 applied"}`},
			},
			"run-fac": {
				{ID: "t1", RunID: "run-fac", StepID: "impl", Type: "agent", Status: "DONE", Attempts: 2, CreatedAt: 1000, UpdatedAt: 5000, Payload: `{"agent":"ux"}`},
				{ID: "t2", RunID: "run-fac", StepID: "impl2", Type: "agent", Status: "FAILED", Attempts: 1, CreatedAt: 1000, UpdatedAt: 2000, Error: "agent stalled: no streamed output for 400ms"},
				{ID: "tg", RunID: "run-fac", StepID: "gate", Type: "human_gate", Status: "FAILED", Result: `{"success":false,"output":{"rejected":true,"reason":"missing empty states"},"detail":"missing empty states"}`},
				{ID: "tg2", RunID: "run-fac", StepID: "gate2", Type: "human_gate", Status: "DONE", Result: `{"success":true,"output":{"approved":true}}`},
			},
			"run-rev": {
				{ID: "t-rev", RunID: "run-rev", StepID: "close", Type: "review_close", Status: "DONE",
					Result: `{"success":true,"output":{"sprint_id":"SP1","decision":"accepted_with_corrections","corrections":["FIX-1"]}}`},
			},
			"run-story-fail": {
				{ID: "tf", RunID: "run-story-fail", StepID: "impl", Type: "agent", Status: "FAILED", Error: "compile error: undefined symbol"},
			},
		},
		events: map[string][]*store.Event{
			"run-fac": {
				{TaskID: "gate3", Type: store.EventStepAnswer, Data: `{"answers":"use Firebase, not Postgres"}`},
			},
		},
	}
	stories := &fakeStories{stories: []tickets.Story{
		{ID: "S1", SprintID: "SP1", Status: tickets.StatusDone},
		{ID: "S2", SprintID: "SP1", Status: tickets.StatusFailed, RunID: "run-story-fail"},
		{ID: "S3", SprintID: "SP1", Status: tickets.StatusCancelled},
	}}
	spend := func(_ string, taskIDs []string) (float64, error) {
		return 1.25 * float64(len(taskIDs)) / float64(len(taskIDs)), nil // deterministic 1.25
	}

	agg := Aggregator{Runs: runs, Stories: stories, Spend: spend}
	js, err := agg.SprintJSON("p1", "SP1")
	if err != nil {
		t.Fatal(err)
	}
	var tel Sprint
	if err := json.Unmarshal([]byte(js), &tel); err != nil {
		t.Fatalf("telemetry not valid JSON: %v", err)
	}

	// Only SP1 runs are included (run-other on SP9 is excluded).
	if len(tel.Runs) != 3 {
		t.Fatalf("want 3 SP1 runs, got %d", len(tel.Runs))
	}
	// (1) per-step: retry + stall surfaced.
	var sawRetry, sawStall bool
	for _, r := range tel.Runs {
		for _, s := range r.Steps {
			if s.Attempts == 2 {
				sawRetry = true
			}
			if s.Stalled {
				sawStall = true
			}
		}
	}
	if !sawRetry || !sawStall {
		t.Errorf("per-step telemetry missing retry(%v)/stall(%v)", sawRetry, sawStall)
	}
	// (2) gates: a reject with text, an approve, and an answer with text.
	var reject, answer, approve bool
	for _, g := range tel.Gates {
		switch g.Decision {
		case "reject":
			reject = g.Text == "missing empty states"
		case "answer":
			answer = strings.Contains(g.Text, "Firebase")
		case "approve":
			approve = true
		}
	}
	if !reject || !answer || !approve {
		t.Errorf("gate decisions missing reject(%v)/answer(%v)/approve(%v): %+v", reject, answer, approve, tel.Gates)
	}
	// (3) plan actions.
	if tel.Plan == nil || tel.Plan.Actions != 3 {
		t.Errorf("plan actions = %+v, want 3", tel.Plan)
	}
	// (4) review decision + corrections.
	if tel.Review == nil || tel.Review.Decision != "accepted_with_corrections" || len(tel.Review.Corrections) != 1 {
		t.Errorf("review telemetry = %+v", tel.Review)
	}
	// (5) story outcomes + the failure cause.
	if tel.Stories.Done != 1 || tel.Stories.Failed != 1 || tel.Stories.Cancelled != 1 {
		t.Errorf("story counts = %+v", tel.Stories)
	}
	if len(tel.Stories.Failures) != 1 || !strings.Contains(tel.Stories.Failures[0].Cause, "compile error") {
		t.Errorf("story failure cause missing: %+v", tel.Stories.Failures)
	}
	// spend.
	if tel.SpendUSD != 1.25 {
		t.Errorf("spend = %v, want 1.25", tel.SpendUSD)
	}
}

// TestSprintTelemetryRespectsCap: a noisy sprint (many runs) is truncated to the cap
// with a note, so the document stays a safe LLM input.
func TestSprintTelemetryRespectsCap(t *testing.T) {
	runs := &fakeRuns{tasks: map[string][]*store.Task{}, events: map[string][]*store.Event{}}
	for i := 0; i < maxRuns+15; i++ {
		id := fmt.Sprintf("run-%d", i)
		runs.runs = append(runs.runs, &store.Run{ID: id, WorkflowID: "factory", Status: "DONE", Payload: payload("SP1", "")})
	}
	agg := Aggregator{Runs: runs, Stories: &fakeStories{}}
	js, _ := agg.SprintJSON("p1", "SP1")
	var tel Sprint
	_ = json.Unmarshal([]byte(js), &tel)

	if len(tel.Runs) != maxRuns {
		t.Fatalf("runs should be capped at %d, got %d", maxRuns, len(tel.Runs))
	}
	if !tel.Truncated {
		t.Error("truncated flag should be set when runs exceed the cap")
	}
	if len(tel.Notes) == 0 || !strings.Contains(tel.Notes[0], "omitted") {
		t.Errorf("expected an omitted-count note, got %v", tel.Notes)
	}
}

// TestSprintTelemetryClipsLongText: free text (a huge error) is clipped to the cap.
func TestSprintTelemetryClipsLongText(t *testing.T) {
	huge := strings.Repeat("x", maxText*3)
	runs := &fakeRuns{
		runs: []*store.Run{{ID: "r", WorkflowID: "factory", Status: "DONE", Payload: payload("SP1", "")}},
		tasks: map[string][]*store.Task{
			"r": {{ID: "t", RunID: "r", StepID: "s", Type: "agent", Status: "FAILED", Error: huge}},
		},
		events: map[string][]*store.Event{},
	}
	agg := Aggregator{Runs: runs, Stories: &fakeStories{}}
	js, _ := agg.SprintJSON("p1", "SP1")
	if len(js) > maxText*4 { // generously bounded — the huge error must have been clipped
		t.Fatalf("telemetry not bounded: %d bytes", len(js))
	}
}
