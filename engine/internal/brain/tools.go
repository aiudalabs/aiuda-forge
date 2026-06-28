package brain

import (
	"encoding/json"
	"fmt"
)

// ToolKind classifies a tool by blast radius. Reversible tools run automatically;
// mutating ones are proposed to the human and run only on approval.
type ToolKind string

const (
	Reversible ToolKind = "reversible"
	Mutating   ToolKind = "mutating"
)

// roleRank orders the v1.1→v1.2 roles. v1.1 callers are always "owner"; the rank
// is the hook v1.2's owner/editor/viewer gating plugs into without touching tools.
var roleRank = map[string]int{"viewer": 1, "editor": 2, "owner": 3}

func roleAllows(min, have string) bool { return roleRank[have] >= roleRank[min] }

// toolDef is a registered tool: its LLM-facing schema, its kind, the minimum role
// to invoke it, and the in-process action.
type toolDef struct {
	Tool    Tool
	Kind    ToolKind
	MinRole string
	Run     func(ops ControlOps, projectID string, input json.RawMessage) (string, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func strp(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// args unmarshals a tool's input JSON into a map (empty on null/"{}").
func args(input json.RawMessage) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(input, &m)
	return m
}
func argStr(input json.RawMessage, key string) string {
	if v, ok := args(input)[key].(string); ok {
		return v
	}
	return ""
}

// registry is the Brain's tool catalog, keyed by tool name.
var registry = map[string]toolDef{
	"get_status": {
		Tool: Tool{Name: "get_status", Description: "Whether the factory engine is paused, and until when.", InputSchema: obj(nil)},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, _ string, _ json.RawMessage) (string, error) {
			paused, until := ops.Status()
			return jsonStr(map[string]any{"paused": paused, "paused_until": until}), nil
		},
	},
	"get_metrics": {
		Tool: Tool{Name: "get_metrics", Description: "Run counts by status for this project — a progress summary.", InputSchema: obj(nil)},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, projectID string, _ json.RawMessage) (string, error) {
			m, err := ops.Metrics(projectID)
			return jsonStr(m), err
		},
	},
	"list_runs": {
		Tool: Tool{Name: "list_runs", Description: "List this project's runs (id, workflow, status).", InputSchema: obj(nil)},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, projectID string, _ json.RawMessage) (string, error) {
			rs, err := ops.ListRuns(projectID)
			return jsonStr(rs), err
		},
	},
	"get_run": {
		Tool: Tool{Name: "get_run", Description: "A run's detail incl. its steps and any failed-step error — use to diagnose a failure.", InputSchema: obj(map[string]any{"run_id": strp("the run id")}, "run_id")},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			r, err := ops.GetRun(argStr(input, "run_id"))
			return jsonStr(r), err
		},
	},
	"pause": {
		Tool: Tool{Name: "pause", Description: "Pause the factory engine (stops claiming new work). Reversible.", InputSchema: obj(nil)},
		Kind: Reversible, MinRole: "editor",
		Run: func(ops ControlOps, _ string, _ json.RawMessage) (string, error) {
			ops.Pause()
			return `{"paused":true}`, nil
		},
	},
	"resume": {
		Tool: Tool{Name: "resume", Description: "Resume the factory engine.", InputSchema: obj(nil)},
		Kind: Reversible, MinRole: "editor",
		Run: func(ops ControlOps, _ string, _ json.RawMessage) (string, error) {
			ops.Resume()
			return `{"paused":false}`, nil
		},
	},
	"cancel_run": {
		Tool: Tool{Name: "cancel_run", Description: "Cancel a run.", InputSchema: obj(map[string]any{"run_id": strp("the run id")}, "run_id")},
		Kind: Reversible, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			return ok(ops.CancelRun(argStr(input, "run_id")))
		},
	},
	"retry_run": {
		Tool: Tool{Name: "retry_run", Description: "Retry a failed run from where it failed.", InputSchema: obj(map[string]any{"run_id": strp("the run id")}, "run_id")},
		Kind: Reversible, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			return ok(ops.RetryRun(argStr(input, "run_id")))
		},
	},
	"requeue_run": {
		Tool: Tool{Name: "requeue_run", Description: "Resurrect a failed run's stories back to the backlog so the orchestrator re-fires them.", InputSchema: obj(map[string]any{"run_id": strp("the run id")}, "run_id")},
		Kind: Reversible, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			n, err := ops.RequeueRun(argStr(input, "run_id"))
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"requeued": n}), nil
		},
	},

	// ---- mutating: proposed to the human, run only on approval ----------------
	"launch_run": {
		Tool: Tool{Name: "launch_run", Description: "Launch a workflow run for this project (e.g. re-run design, or iterate to add a feature). MUTATING — proposed for human approval.", InputSchema: obj(map[string]any{
			"workflow":     strp("workflow id, e.g. design | factory | iterate"),
			"instructions": strp("what to build / the change request"),
		}, "workflow")},
		Kind: Mutating, MinRole: "editor",
		Run: func(ops ControlOps, projectID string, input json.RawMessage) (string, error) {
			a := args(input)
			payload := map[string]any{"project_id": projectID}
			if s, ok := a["instructions"].(string); ok && s != "" {
				payload["instructions"] = s
			}
			wf, _ := a["workflow"].(string)
			runID, err := ops.StartRun(wf, payload)
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"run_id": runID, "status": "QUEUED"}), nil
		},
	},
	"approve_step": {
		Tool: Tool{Name: "approve_step", Description: "Approve a run's awaiting human-gate step. MUTATING — proposed for human approval.", InputSchema: obj(map[string]any{"run_id": strp("run id"), "step": strp("step id")}, "run_id", "step")},
		Kind: Mutating, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			return ok(ops.ApproveStep(argStr(input, "run_id"), argStr(input, "step")))
		},
	},
	"reject_step": {
		Tool: Tool{Name: "reject_step", Description: "Reject a run's awaiting step with feedback (loops it back). MUTATING — proposed for human approval.", InputSchema: obj(map[string]any{"run_id": strp("run id"), "step": strp("step id"), "reason": strp("feedback")}, "run_id", "step")},
		Kind: Mutating, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			return ok(ops.RejectStep(argStr(input, "run_id"), argStr(input, "step"), argStr(input, "reason")))
		},
	},
}

// toolList returns the LLM-facing tool schemas (stable order not required).
func toolList() []Tool {
	out := make([]Tool, 0, len(registry))
	for _, d := range registry {
		out = append(out, d.Tool)
	}
	return out
}

func jsonStr(v any) string { b, _ := json.Marshal(v); return string(b) }
func ok(err error) (string, error) {
	if err != nil {
		return "", err
	}
	return `{"ok":true}`, nil
}

// lookup returns a tool def by name.
func lookup(name string) (toolDef, error) {
	d, ok := registry[name]
	if !ok {
		return toolDef{}, fmt.Errorf("unknown tool %q", name)
	}
	return d, nil
}
