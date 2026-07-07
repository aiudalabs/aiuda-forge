package github

import (
	"errors"
	"testing"
)

// TestIsForbidden es el crux del fix de Bug A: la Agent tasks API devuelve un 403 con
// mensaje DISTINTO por verbo — "does not have read access" en el GET de disponibilidad,
// pero un simple "forbidden" en el POST de creación. isForbidden debe reconocer AMBOS
// (y "HTTP 403") para que la degradación al host dispare en el path de creación, que es
// donde el bug hacía caer Copilot a claude_action.
func TestIsForbidden(t *testing.T) {
	yes := []string{
		"gh api create agent task: exit status 1: {\"message\":\"forbidden\"}\ngh: forbidden (HTTP 403)",
		"gh api create agent task: exit status 1: forbidden: user does not have read access to the repository (HTTP 403)",
		"something HTTP 403 something",
		"FORBIDDEN in caps",
	}
	for _, m := range yes {
		if !isForbidden(errors.New(m)) {
			t.Errorf("isForbidden(%q) = false, want true", m)
		}
	}
	no := []error{
		nil,
		errors.New("gh api: exit status 1: {\"message\":\"Not Found\"} (HTTP 404)"),
		errors.New("validation failed: prompt is required (HTTP 422)"),
		errors.New("connection refused"),
	}
	for _, e := range no {
		if isForbidden(e) {
			t.Errorf("isForbidden(%v) = true, want false", e)
		}
	}
}
