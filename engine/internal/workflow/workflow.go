// Package workflow parses workflow manifests (YAML data) and runs them through a
// GENERIC executor. Per golden rule #1 there is no per-flow branching here: the
// executor interprets step *types* declared in data. Adding/removing an agent is
// editing a YAML file, never editing Go.
package workflow

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Workflow is the parsed manifest: an ordered list of steps. Default control
// flow is linear (the step after the current one); on_fail overrides with a goto.
type Workflow struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Steps   []Step `yaml:"steps"`

	index map[string]int // step id -> position, built on parse
}

// Step is one node. Type selects the kernel step-runner; the rest are data the
// runner interprets. Unknown-to-the-kernel fields are tolerated (forward-compat).
type Step struct {
	ID   string `yaml:"id"`
	Type string `yaml:"type"` // echo|agent|gate|pr|agentic_verify|human_gate

	// agent steps
	Agent  string `yaml:"agent"`
	Model  string `yaml:"model"`  // cross-model override (reviewer != implementer)
	Prompt string `yaml:"prompt"` // prompt style hint, e.g. "adversarial"

	// gate steps
	Command     string `yaml:"command"`      // inline command (tests)
	CommandFrom string `yaml:"command_from"` // "repo" -> read .vibeforge-gate from workdir

	// generic
	Inputs map[string]any `yaml:"inputs"`
	OnFail *OnFail        `yaml:"on_fail"`
	// SkipIfEmpty names input keys that gate this step on the LINEAR (success) path:
	// when the step is reached by normal advance and ALL of these inputs resolve empty,
	// the step is skipped as a zero-cost no-op (no task, no runner, no LLM call) and
	// the flow advances past it. A step reached via on_fail.goto or the answer verb is
	// enqueued DIRECTLY (with feedback/answers injected), so it still runs there. This
	// is what lets a reject-loop body (e.g. a corrections step) sit before its gate for
	// the loop-back to work, yet cost nothing on the happy path where it is never
	// rejected. Generic + data-driven — no step-id knowledge in the executor.
	SkipIfEmpty []string `yaml:"skip_if_empty"`
}

// OnFail is the generalized retry/loop primitive: on failure, jump to `Goto` up
// to `Max` times, optionally injecting `Feedback` (a ref like $gate.detail) into
// the target step's inputs. This single construct covers gate-fix AND qa->dev.
type OnFail struct {
	Goto     string `yaml:"goto"`
	Max      int    `yaml:"max"`
	Feedback any    `yaml:"feedback"`
}

// Parse decodes a workflow manifest from bytes and validates structure.
func Parse(data []byte) (*Workflow, error) {
	var wf Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if wf.ID == "" {
		return nil, fmt.Errorf("workflow missing id")
	}
	if len(wf.Steps) == 0 {
		return nil, fmt.Errorf("workflow %s has no steps", wf.ID)
	}
	wf.index = map[string]int{}
	for i, s := range wf.Steps {
		if s.ID == "" {
			return nil, fmt.Errorf("workflow %s: step %d missing id", wf.ID, i)
		}
		if _, dup := wf.index[s.ID]; dup {
			return nil, fmt.Errorf("workflow %s: duplicate step id %q", wf.ID, s.ID)
		}
		if s.Type == "" {
			return nil, fmt.Errorf("workflow %s: step %q missing type", wf.ID, s.ID)
		}
		wf.index[s.ID] = i
	}
	// Validate on_fail gotos point at real steps.
	for _, s := range wf.Steps {
		if s.OnFail != nil && s.OnFail.Goto != "" {
			if _, ok := wf.index[s.OnFail.Goto]; !ok {
				return nil, fmt.Errorf("workflow %s: step %q on_fail.goto %q is not a step", wf.ID, s.ID, s.OnFail.Goto)
			}
		}
	}
	return &wf, nil
}

// ParseFile reads and parses a workflow manifest from disk.
func ParseFile(path string) (*Workflow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// StepByID returns the step with the given id, or false.
func (w *Workflow) StepByID(id string) (Step, bool) {
	i, ok := w.index[id]
	if !ok {
		return Step{}, false
	}
	return w.Steps[i], true
}

// First returns the first step.
func (w *Workflow) First() Step { return w.Steps[0] }

// Next returns the step after id in declaration order, or false if id is the last.
func (w *Workflow) Next(id string) (Step, bool) {
	i, ok := w.index[id]
	if !ok || i+1 >= len(w.Steps) {
		return Step{}, false
	}
	return w.Steps[i+1], true
}
