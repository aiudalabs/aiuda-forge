package brain

import (
	"encoding/json"
	"fmt"
	"os"
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
	"get_state": {
		Tool: Tool{Name: "get_state", Description: "The CURRENT live state of this project, digested: whether the engine is paused, the ACTIVE runs (queued/running/awaiting), the steps awaiting approval, and a count of old terminal runs. ALWAYS call this to answer 'how is it going / what's running / is it paused' — do NOT infer state from list_runs (which dumps every old failed/cancelled run and leads to wrong conclusions).", InputSchema: obj(nil)},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, projectID string, _ json.RawMessage) (string, error) {
			st, err := ops.ActiveState(projectID)
			return jsonStr(st), err
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
	"read_artifact": {
		Tool: Tool{Name: "read_artifact", Description: "Read the DOCUMENT a run's step produced (e.g. the PRD, the data model, the backlog, a mockup). Returns the doc text. Use to REVIEW or diagnose what a phase actually generated — don't guess its content.", InputSchema: obj(map[string]any{"run_id": strp("the run id"), "step": strp("the step id, e.g. discovery | prd | data_model | architecture | ui | backlog | mockups")}, "run_id", "step")},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			c, err := ops.Artifact(argStr(input, "run_id"), argStr(input, "step"))
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"content": c}), nil
		},
	},
	"get_run_events": {
		Tool: Tool{Name: "get_run_events", Description: "The timeline of a run's step events (seq, type, task, time) — use to see progress or diagnose WHERE/WHEN a run stalled or failed.", InputSchema: obj(map[string]any{"run_id": strp("the run id")}, "run_id")},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			evs, err := ops.RunEvents(argStr(input, "run_id"))
			return jsonStr(evs), err
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
	"rerun_step": {
		Tool: Tool{Name: "rerun_step", Description: "Re-run a SINGLE step of an existing run IN PLACE — regenerate one phase (e.g. mockups after changing the designer's model) reusing the run's existing docs, WITHOUT re-running the whole design or cascading to downstream steps (no GitHub re-publish). Overwrites that phase's artifact. MUTATING — proposed for human approval.", InputSchema: obj(map[string]any{"run_id": strp("the run id"), "step": strp("the step to re-run, e.g. mockups | ui | prd | data_model")}, "run_id", "step")},
		Kind: Mutating, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			return ok(ops.RerunStep(argStr(input, "run_id"), argStr(input, "step")))
		},
	},

	// ---- registry / method authoring -----------------------------------------
	// The registry IS the methodology, as data. These let the Brain author it:
	// create a new flow, edit a persona, change a model, add a skill — the same
	// edits a human operator makes by hand. read/list are reversible; write/delete
	// are mutating and proposed for approval.
	"list_registry": {
		Tool: Tool{Name: "list_registry", Description: "List the ids of registry items of a kind. kind = workflow (a flow) | agent (a persona's manifest: model/skills/tools/role) | agent_persona (a persona's prompt markdown) | skill (a method fragment).", InputSchema: obj(map[string]any{"kind": strp("workflow | agent | agent_persona | skill")}, "kind")},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			ids, err := ops.ListRegistry(argStr(input, "kind"))
			return jsonStr(ids), err
		},
	},
	"read_registry": {
		Tool: Tool{Name: "read_registry", Description: "Read the raw content of a registry item (YAML for workflow/agent, markdown for agent_persona/skill). Call before editing so you replace the FULL file, not a fragment.", InputSchema: obj(map[string]any{"kind": strp("workflow | agent | agent_persona | skill"), "id": strp("the item id")}, "kind", "id")},
		Kind: Reversible, MinRole: "viewer",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			c, err := ops.ReadRegistry(argStr(input, "kind"), argStr(input, "id"))
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"content": c}), nil
		},
	},
	"write_registry": {
		Tool: Tool{Name: "write_registry", Description: "Create or replace a registry item — this AUTHORS THE METHOD: a new workflow, an edited persona, a changed model, a new skill. `content` is the FULL file (YAML for workflow/agent, markdown for agent_persona/skill). Validated with the kernel's own parser (an invalid workflow/agent is rejected); a written workflow goes live for the next run. MUTATING — proposed for human approval.", InputSchema: obj(map[string]any{"kind": strp("workflow | agent | agent_persona | skill"), "id": strp("the item id"), "content": strp("the FULL file content")}, "kind", "id", "content")},
		Kind: Mutating, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			a := args(input)
			kind, _ := a["kind"].(string)
			id, _ := a["id"].(string)
			content, _ := a["content"].(string)
			return ok(ops.WriteRegistry(kind, id, content))
		},
	},
	"delete_registry": {
		Tool: Tool{Name: "delete_registry", Description: "Delete a registry item (workflow/agent/agent_persona/skill). MUTATING — proposed for human approval.", InputSchema: obj(map[string]any{"kind": strp("workflow | agent | agent_persona | skill"), "id": strp("the item id")}, "kind", "id")},
		Kind: Mutating, MinRole: "editor",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			return ok(ops.DeleteRegistry(argStr(input, "kind"), argStr(input, "id")))
		},
	},

	// ---- escape hatch (opt-in) ------------------------------------------------
	// The long tail: anything no specific tool covers (gh, git, docker, scripts).
	// Owner-only, mutating (the user sees the exact command before it runs), and
	// OFFERED ONLY when VIBEFORGE_BRAIN_EXEC=1 — off by default.
	"exec": {
		Tool: Tool{Name: "exec", Description: "Run a shell command on the control host and return its combined output. The ESCAPE HATCH for anything no specific tool covers (gh, git, docker, scripts). MUTATING and owner-only — proposed for human approval; the user sees the exact command before it runs.", InputSchema: obj(map[string]any{"command": strp("the shell command to run")}, "command")},
		Kind: Mutating, MinRole: "owner",
		Run: func(ops ControlOps, _ string, input json.RawMessage) (string, error) {
			return ops.Exec(argStr(input, "command"))
		},
	},
}

// toolList returns the LLM-facing tool schemas (stable order not required).
func toolList() []Tool {
	out := make([]Tool, 0, len(registry))
	for name, d := range registry {
		if !toolEnabled(name) {
			continue
		}
		out = append(out, d.Tool)
	}
	return out
}

// toolEnabled gates opt-in tools. The exec escape hatch is offered ONLY when the
// deployment sets VIBEFORGE_BRAIN_EXEC=1 — otherwise the Brain never sees it, and
// execTool refuses it as a second line of defence.
func toolEnabled(name string) bool {
	if name == "exec" {
		return os.Getenv("VIBEFORGE_BRAIN_EXEC") == "1"
	}
	return true
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
