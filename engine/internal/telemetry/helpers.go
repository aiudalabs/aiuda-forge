package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"
)

// clip trims free text to maxText runes (telemetry is an LLM input — long errors /
// feedback are truncated, not dropped).
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxText {
		return s
	}
	return s[:maxText] + "…"
}

// containsAny reports whether s contains any of subs (case-insensitive).
func containsAny(s string, subs ...string) bool {
	ls := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(ls, sub) {
			return true
		}
	}
	return false
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// payloadStr extracts a top-level string field from a task's input payload JSON.
func payloadStr(payload, key string) string {
	var p map[string]any
	if payload == "" || json.Unmarshal([]byte(payload), &p) != nil {
		return ""
	}
	s, _ := p[key].(string)
	return s
}

// resultMap parses a step result JSON ({success, output, detail}) into a map.
func resultMap(result string) map[string]any {
	var r map[string]any
	if result == "" || json.Unmarshal([]byte(result), &r) != nil {
		return nil
	}
	return r
}

func resultDetail(result string) string {
	if r := resultMap(result); r != nil {
		if d, ok := r["detail"].(string); ok {
			return d
		}
	}
	return ""
}

// resultOutputStr reads result.output[key] as a string.
func resultOutputStr(result, key string) string {
	if out := resultOutput(result); out != nil {
		if s, ok := out[key].(string); ok {
			return s
		}
	}
	return ""
}

// resultOutputInt reads result.output[key] as an int (JSON numbers decode to float64).
func resultOutputInt(result, key string) int {
	if out := resultOutput(result); out != nil {
		if f, ok := out[key].(float64); ok {
			return int(f)
		}
	}
	return 0
}

// resultOutputStrings reads result.output[key] as a []string.
func resultOutputStrings(result, key string) []string {
	out := resultOutput(result)
	if out == nil {
		return nil
	}
	raw, ok := out[key].([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			ids = append(ids, s)
		}
	}
	return ids
}

func resultOutput(result string) map[string]any {
	if r := resultMap(result); r != nil {
		if out, ok := r["output"].(map[string]any); ok {
			return out
		}
	}
	return nil
}

// answerText pulls the human's answer text out of a step.answer event's Data. The
// event stores the text under a well-known key; accept a couple of shapes defensively.
func answerText(data string) string {
	var d map[string]any
	if data == "" || json.Unmarshal([]byte(data), &d) != nil {
		return strings.TrimSpace(data)
	}
	for _, k := range []string{"answers", "answer", "text"} {
		if s, ok := d[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
