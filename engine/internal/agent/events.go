package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"forge/internal/store"
	"forge/internal/workflow"
)

// Truncation bounds keep a single step.event row small even when the agent edits
// a huge file or emits a wall of text — the live-log stays an auditable trail,
// not a copy of the repo. Tool inputs are summarised tighter than assistant text.
const (
	maxToolInputLen = 600
	maxTextLen      = 2000
)

// eventSink builds the onEvent callback passed to Backend.Run. It fans each
// streamed agent Event to BOTH the context-threaded step emitter (the real,
// per-run path that persists step.event rows) and the legacy r.Emit field (kept
// working as a belt-and-suspenders hook for callers that set it). Either may be
// nil. If neither is present it returns nil so the backend skips event work.
func eventSink(ctx context.Context, legacy func(Event)) func(Event) {
	emit := workflow.EmitterFrom(ctx)
	if emit == nil && legacy == nil {
		return nil
	}
	return func(e Event) {
		if legacy != nil {
			legacy(e)
		}
		if emit == nil {
			return
		}
		if data, ok := eventPayload(e); ok {
			emit(store.EventStepEvent, data)
		}
	}
}

// eventPayload converts one streamed agent Event into the bounded, auditable map
// persisted as a step.event row. Returns ok=false for events we deliberately
// drop (the terminal result — already stored as the step detail — and empties).
func eventPayload(e Event) (map[string]any, bool) {
	switch e.Kind {
	case KindToolUse:
		return map[string]any{
			"kind":  "tool_use",
			"tool":  e.Tool,
			"input": toolInputSummary(e.Raw),
		}, true
	case KindText:
		if e.Text == "" {
			return nil, false
		}
		// Redact secrets BEFORE truncation/persistence (audit C4): the live-log is
		// served via GET /runs/{id}/events and must never carry a real token.
		return map[string]any{
			"kind": "text",
			"text": truncate(redactSecrets(e.Text), maxTextLen),
		}, true
	case KindThinking:
		if e.Text == "" {
			return nil, false
		}
		// Truncate thinking heavily — it can be tens of thousands of chars; the
		// board just needs a glimpse to confirm the agent is reasoning, not a
		// transcript of the full thought.
		return map[string]any{
			"kind": "thinking",
			"text": truncate(e.Text, 300),
		}, true
	case KindSystem:
		// Minimal: enough to mark a system notice on the timeline without the
		// noisy init blob (model list, tool inventory, cwd, ...).
		// "backend" is set by the runner's synthetic "engine" event so the UI
		// can show a runtime chip without an extra API call.
		return map[string]any{
			"kind":    "system",
			"subtype": asString(e.Raw["subtype"]),
			"backend": asString(e.Raw["backend"]),
		}, true
	default:
		// KindResult and anything else: the result is already the step detail.
		return nil, false
	}
}

// toolInputSummary renders the tool's "input" object as a compact JSON-ish
// string, truncated. This is what makes the line read like "Edit notas.py" or
// "Bash: python -m unittest" — the raw block carries name + input.
func toolInputSummary(raw map[string]any) string {
	if raw == nil {
		return ""
	}
	in, ok := raw["input"]
	if !ok {
		return ""
	}
	if s, isStr := in.(string); isStr {
		return truncate(redactSecrets(s), maxToolInputLen)
	}
	b, err := json.Marshal(in)
	if err != nil {
		return truncate(redactSecrets(fmt.Sprintf("%v", in)), maxToolInputLen)
	}
	return truncate(redactSecrets(string(b)), maxToolInputLen)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
