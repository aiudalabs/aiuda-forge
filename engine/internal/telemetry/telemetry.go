// Package telemetry aggregates a single sprint's execution facts — retries, stalls,
// durations, spend, gate decisions (with the human's text), the applied plan, the
// review decision, and story outcomes — into ONE compact, BOUNDED JSON document. It
// is the evidence the retrospective's analyst reasons over, and is deliberately
// SIZE-CAPPED because it is fed to an LLM: long lists are truncated with a count and
// free text is clipped, so a noisy sprint can never blow the context window.
//
// One implementation, two callers (kernel rule #4, no privileged path): the
// control-plane exposes it at GET /sprints/{id}/telemetry (the native scheduler injects
// it into the retro run's payload) AND the Brain's get_sprint_telemetry tool calls the
// same Aggregator in-process. Mirrors how the digest composes its data sources.
package telemetry

import (
	"encoding/json"

	"forge/internal/store"
	"forge/internal/tickets"
)

// Caps keep the document small enough to be a safe LLM input. Lists beyond these
// lengths are truncated (with an "omitted" count); free text beyond maxText is clipped.
const (
	maxRuns     = 25
	maxGates    = 40
	maxFailures = 25
	maxText     = 400
)

// RunSource is the control-store surface the aggregator reads (satisfied by
// *store.Store). Runs are matched to a sprint by their trigger payload's sprint_id.
type RunSource interface {
	ListRunsByProject(status store.Status, projectID string) ([]*store.Run, error)
	TasksForRun(runID string) ([]*store.Task, error)
	EventsAfter(runID string, after int64) ([]*store.Event, error)
}

// StorySource is the ticket-store surface (satisfied by *tickets.Store).
type StorySource interface {
	StoriesBySprintScoped(sprintID, projectID string) ([]tickets.Story, error)
}

// SpendFn returns the token cost (USD) of the given tasks for a project (billing:
// project → owner → workspace → sum cost_events by task_id). Optional — nil reports $0.
type SpendFn func(projectID string, taskIDs []string) (float64, error)

// Aggregator gathers a sprint's telemetry. Runs + Stories are required; Spend is optional.
type Aggregator struct {
	Runs    RunSource
	Stories StorySource
	Spend   SpendFn
}

// ---- output shape (compact JSON keys) --------------------------------------

// Sprint is the bounded telemetry document.
type Sprint struct {
	SprintID  string     `json:"sprint_id"`
	Runs      []RunTel   `json:"runs"`
	Gates     []GateTel  `json:"gates"`
	Plan      *PlanTel   `json:"plan,omitempty"`
	Review    *ReviewTel `json:"review,omitempty"`
	Stories   StoriesTel `json:"stories"`
	SpendUSD  float64    `json:"spend_usd"`
	Truncated bool       `json:"truncated,omitempty"`
	Notes     []string   `json:"notes,omitempty"` // e.g. "12 more runs omitted"
}

type RunTel struct {
	RunID    string    `json:"run_id"`
	Workflow string    `json:"workflow"`
	Status   string    `json:"status"`
	Steps    []StepTel `json:"steps"`
}

type StepTel struct {
	Step       string `json:"step"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts,omitempty"` // >1 means it retried
	DurationMs int64  `json:"duration_ms,omitempty"`
	Stalled    bool   `json:"stalled,omitempty"` // idle-watchdog / timeout signal
	Agent      string `json:"agent,omitempty"`
	Error      string `json:"error,omitempty"` // clipped
}

// GateTel is one human decision: approve | reject | answer, with the human's text.
type GateTel struct {
	RunID    string `json:"run_id"`
	Step     string `json:"step"`
	Decision string `json:"decision"` // approve|reject|answer
	Text     string `json:"text,omitempty"`
}

type PlanTel struct {
	RunID   string `json:"run_id"`
	Actions int    `json:"actions"` // count of plan actions applied by plan_apply
	Detail  string `json:"detail,omitempty"`
}

type ReviewTel struct {
	RunID       string   `json:"run_id"`
	Decision    string   `json:"decision"`
	Corrections []string `json:"corrections,omitempty"`
}

type StoriesTel struct {
	Total     int         `json:"total"`
	Done      int         `json:"done"`
	Failed    int         `json:"failed"`
	Cancelled int         `json:"cancelled"`
	Failures  []StoryFail `json:"failures,omitempty"`
}

type StoryFail struct {
	ID    string `json:"id"`
	Cause string `json:"cause,omitempty"` // clipped; from the story's failing task
}

// SprintJSON aggregates the sprint's telemetry and returns it as a JSON string (the
// form both the run payload and the Brain tool return). It never errors on partial
// data — a store read failure for one run is skipped, not fatal — so the retro always
// gets whatever evidence is available.
func (a Aggregator) SprintJSON(projectID, sprintID string) (string, error) {
	tel := a.aggregate(projectID, sprintID)
	b, err := json.Marshal(tel)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (a Aggregator) aggregate(projectID, sprintID string) Sprint {
	out := Sprint{SprintID: sprintID, Runs: []RunTel{}, Gates: []GateTel{}}

	runs, _ := a.Runs.ListRunsByProject("", projectID)
	var sprintTaskIDs []string
	for _, run := range runs {
		if run == nil || !runIsForSprint(run, sprintID) {
			continue
		}
		tasks, _ := a.Runs.TasksForRun(run.ID)
		events, _ := a.Runs.EventsAfter(run.ID, 0)

		// (1) per-step run telemetry.
		rt := RunTel{RunID: run.ID, Workflow: run.WorkflowID, Status: string(run.Status)}
		for _, t := range tasks {
			if t == nil {
				continue
			}
			sprintTaskIDs = append(sprintTaskIDs, t.ID)
			rt.Steps = append(rt.Steps, stepTel(t))
			// (2) gate decisions from human_gate tasks (+ answer events below).
			if t.Type == "human_gate" {
				if g, ok := gateFromTask(run.ID, t); ok {
					out.Gates = appendGate(out.Gates, g, &out)
				}
			}
			// (3) plan actions from a plan_apply step.
			if t.Type == "plan_apply" && t.Status == statusDone {
				out.Plan = planFromTask(run.ID, t)
			}
			// (4) review decision from a review_close step.
			if t.Type == "review_close" && t.Status == statusDone {
				out.Review = reviewFromTask(run.ID, t)
			}
		}
		// answer-verb decisions carry the human's text on step.answer events.
		for _, ev := range events {
			if ev != nil && ev.Type == store.EventStepAnswer {
				out.Gates = appendGate(out.Gates, GateTel{
					RunID: run.ID, Step: ev.TaskID, Decision: "answer", Text: clip(answerText(ev.Data)),
				}, &out)
			}
		}
		if len(out.Runs) < maxRuns {
			out.Runs = append(out.Runs, rt)
		} else {
			out.Truncated = true
		}
	}
	if n := countSprintRuns(runs, sprintID) - len(out.Runs); n > 0 {
		out.Notes = append(out.Notes, plural(n, "run")+" omitted")
	}

	// (5) story outcomes.
	out.Stories = a.storyOutcomes(projectID, sprintID, &out)

	// spend across the sprint's tasks (optional).
	if a.Spend != nil && len(sprintTaskIDs) > 0 {
		if v, err := a.Spend(projectID, sprintTaskIDs); err == nil {
			out.SpendUSD = v
		}
	}
	return out
}

const statusDone = store.Status("DONE")

// runIsForSprint reports whether run's trigger payload names sprintID. Planning,
// review, retro and factory runs of a sprint all carry sprint_id in their payload.
func runIsForSprint(run *store.Run, sprintID string) bool {
	var p map[string]any
	if json.Unmarshal([]byte(run.Payload), &p) != nil {
		return false
	}
	s, _ := p["sprint_id"].(string)
	return s == sprintID
}

func countSprintRuns(runs []*store.Run, sprintID string) int {
	n := 0
	for _, r := range runs {
		if r != nil && runIsForSprint(r, sprintID) {
			n++
		}
	}
	return n
}

func stepTel(t *store.Task) StepTel {
	st := StepTel{Step: t.StepID, Type: t.Type, Status: string(t.Status), Attempts: t.Attempts}
	if t.UpdatedAt > t.CreatedAt {
		st.DurationMs = t.UpdatedAt - t.CreatedAt
	}
	if t.Error != "" {
		st.Error = clip(t.Error)
		st.Stalled = containsAny(t.Error, "stalled", "timeout", "idle", "absolute timeout")
	}
	st.Agent = payloadStr(t.Payload, "agent")
	return st
}

// gateFromTask derives an approve/reject decision from a resolved human_gate task. An
// answer resolves the gate DONE too, but its text lives on a step.answer event, so a
// DONE gate with no answer event is treated as an approve (answers are added
// separately from events, and a duplicate is harmless — the retro reads patterns).
func gateFromTask(runID string, t *store.Task) (GateTel, bool) {
	switch t.Status {
	case "FAILED":
		return GateTel{RunID: runID, Step: t.StepID, Decision: "reject", Text: clip(gateReason(t))}, true
	case statusDone:
		return GateTel{RunID: runID, Step: t.StepID, Decision: "approve"}, true
	}
	return GateTel{}, false
}

// gateReason pulls the rejection feedback from a FAILED gate task (Result.output.reason,
// else Result.detail, else Error).
func gateReason(t *store.Task) string {
	if r := resultOutputStr(t.Result, "reason"); r != "" {
		return r
	}
	if d := resultDetail(t.Result); d != "" {
		return d
	}
	return t.Error
}

func planFromTask(runID string, t *store.Task) *PlanTel {
	return &PlanTel{RunID: runID, Actions: resultOutputInt(t.Result, "actions"), Detail: clip(resultDetail(t.Result))}
}

func reviewFromTask(runID string, t *store.Task) *ReviewTel {
	return &ReviewTel{
		RunID:       runID,
		Decision:    resultOutputStr(t.Result, "decision"),
		Corrections: resultOutputStrings(t.Result, "corrections"),
	}
}

func (a Aggregator) storyOutcomes(projectID, sprintID string, out *Sprint) StoriesTel {
	st := StoriesTel{}
	stories, _ := a.Stories.StoriesBySprintScoped(sprintID, projectID)
	st.Total = len(stories)
	for _, s := range stories {
		switch s.Status {
		case tickets.StatusDone:
			st.Done++
		case tickets.StatusCancelled:
			st.Cancelled++
		case tickets.StatusFailed:
			st.Failed++
			if len(st.Failures) < maxFailures {
				st.Failures = append(st.Failures, StoryFail{ID: s.ID, Cause: clip(a.failCause(s))})
			} else {
				out.Truncated = true
			}
		}
	}
	return st
}

// failCause reads the failing story's run and returns its last FAILED task's error —
// the concrete cause the retro cites. Best-effort: empty when unavailable.
func (a Aggregator) failCause(s tickets.Story) string {
	if s.RunID == "" {
		return ""
	}
	tasks, err := a.Runs.TasksForRun(s.RunID)
	if err != nil {
		return ""
	}
	cause := ""
	for _, t := range tasks {
		if t != nil && t.Status == "FAILED" && t.Error != "" {
			cause = t.Error // last one wins
		}
	}
	return cause
}

func appendGate(gates []GateTel, g GateTel, out *Sprint) []GateTel {
	if len(gates) >= maxGates {
		out.Truncated = true
		return gates
	}
	return append(gates, g)
}
