package brain

import (
	"encoding/json"
	"fmt"

	"forge/internal/store"
)

// bestTask picks the artifact-bearing instance of a step: the latest DONE, else the
// latest attempt (tasks arrive created_at ASC). Mirrors the API artifacts handler (#18):
// a retry leaves an older FAILED task plus a newer DONE — return the one that produced.
func bestTask(tasks []*store.Task, stepID string) *store.Task {
	var best *store.Task
	for _, t := range tasks {
		if t.StepID != stepID {
			continue
		}
		if best == nil || t.Status == store.StatusDone || best.Status != store.StatusDone {
			best = t
		}
	}
	return best
}

// docText pulls the produced document out of a step result (result.output.text),
// falling back to the raw result JSON when there is no doc field.
func docText(result string) string {
	var res struct {
		Output struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if json.Unmarshal([]byte(result), &res) == nil && res.Output.Text != "" {
		return res.Output.Text
	}
	return result
}

func (o EngineOps) Artifact(runID, stepID string) (string, error) {
	tasks, err := o.Store.TasksForRun(runID)
	if err != nil {
		return "", err
	}
	best := bestTask(tasks, stepID)
	if best == nil {
		return "", fmt.Errorf("no artifact for step %q in run %q", stepID, runID)
	}
	return docText(best.Result), nil
}

func (o EngineOps) RunEvents(runID string) ([]map[string]any, error) {
	evs, err := o.Store.EventsAfter(runID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		out = append(out, map[string]any{
			"seq": e.Seq, "type": e.Type, "task": e.TaskID, "at": e.CreatedAt,
		})
	}
	return out, nil
}
