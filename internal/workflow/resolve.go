package workflow

import (
	"fmt"
	"strings"
)

// Context holds the values a step's inputs can reference: the trigger payload
// under "trigger", and each completed step's result under its step id, e.g.
//
//	{"trigger": {"ticket": "..."}, "implement": {"output": {...}, "detail": "...", "success": true}}
type Context map[string]any

// resolveValue resolves a single input value. Strings beginning with "$" are
// references into the Context via a dotted path ($trigger.ticket, $gate.detail,
// $implement.output.path). Everything else passes through unchanged. A reference
// that does not resolve yields "" (string) rather than an error, so a missing
// optional feedback does not block a flow.
func resolveValue(v any, ctx Context) any {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, "$") {
		return v
	}
	path := strings.Split(strings.TrimPrefix(s, "$"), ".")
	var cur any = map[string]any(ctx)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		nxt, ok := m[key]
		if !ok {
			return ""
		}
		cur = nxt
	}
	return cur
}

// ResolveInputs resolves every value in a step's inputs against the context.
func ResolveInputs(inputs map[string]any, ctx Context) map[string]any {
	out := map[string]any{}
	for k, v := range inputs {
		out[k] = resolveValue(v, ctx)
	}
	return out
}

// asString best-effort renders a resolved value as a string for prompts/feedback.
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}
