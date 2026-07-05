package brain

import (
	"testing"

	"forge/internal/store"
)

func TestBestTaskPrefersDoneOverLaterFailed(t *testing.T) {
	// A retry leaves an older FAILED plus a newer DONE (or vice-versa). The artifact
	// is whatever DONE produced, never a stale FAILED (#18).
	tasks := []*store.Task{
		{StepID: "ui", Status: store.StatusFailed, Result: "old-failed"},
		{StepID: "prd", Status: store.StatusDone, Result: "prd-doc"},
		{StepID: "ui", Status: store.StatusDone, Result: "ui-done"},
		{StepID: "ui", Status: store.StatusFailed, Result: "later-failed"},
	}
	if b := bestTask(tasks, "ui"); b == nil || b.Result != "ui-done" {
		t.Fatalf("ui best: %+v", b)
	}
	if b := bestTask(tasks, "prd"); b == nil || b.Result != "prd-doc" {
		t.Fatalf("prd best: %+v", b)
	}
	if bestTask(tasks, "missing") != nil {
		t.Fatal("missing step should have no best task")
	}
}

func TestDocTextExtractsOutput(t *testing.T) {
	res := `{"detail":"summary","output":{"text":"# PRD\nhello","agent":"pm"},"success":true}`
	if got := docText(res); got != "# PRD\nhello" {
		t.Fatalf("docText: %q", got)
	}
	raw := `{"detail":"x"}` // no output.text → return raw
	if got := docText(raw); got != raw {
		t.Fatalf("fallback: %q", got)
	}
}
