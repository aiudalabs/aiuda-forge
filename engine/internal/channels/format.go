package channels

import "encoding/json"

// Notable event types delivered to channels. Other event types (the high-volume
// step.* stream) are intentionally NOT delivered — channels get the milestones a
// human cares about, not the firehose.
const (
	EvtRunFailed       = "run.failed"
	EvtRunAwaiting     = "run.awaiting_approval"
	EvtRunDone         = "run.done"
	EvtSpendCapTripped = "billing.spend_cap_tripped"
)

// Format maps a raw factory event to a channel-facing Event, or returns ok=false if
// the type is not notable. data is the event's JSON payload (may be nil/empty).
func Format(eventType, projectID, runID string, data []byte) (Event, bool) {
	var d map[string]any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &d)
	}
	ev := Event{Type: eventType, ProjectID: projectID, RunID: runID}
	switch eventType {
	case EvtRunFailed:
		ev.Title = "❌ Run falló — " + projectID
		ev.Detail = "run " + runID + field(d, "error", " · ")
	case EvtRunAwaiting:
		ev.Title = "⏸ Espera aprobación — " + projectID
		ev.Detail = "run " + runID + field(d, "step", " · paso ")
	case EvtRunDone:
		ev.Title = "✅ Run completado — " + projectID
		ev.Detail = "run " + runID
	case EvtSpendCapTripped:
		ev.Title = "💸 Tope de gasto alcanzado — workspace pausado"
		ev.Detail = field(d, "workspace", "workspace ")
	default:
		return Event{}, false
	}
	return ev, true
}

// field renders " <prefix><value>" when key is a non-empty string in d, else "".
func field(d map[string]any, key, prefix string) string {
	if d == nil {
		return ""
	}
	if v, ok := d[key].(string); ok && v != "" {
		return prefix + v
	}
	return ""
}
