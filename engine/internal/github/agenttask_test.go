package github

import (
	"context"
	"errors"
	"testing"
)

// TestAgentTaskStateNotFound: a 404 "not found" (a purged/ephemeral task) surfaces
// as ErrTaskNotFound so the conductor can distinguish a dead task from a transient
// error. execRunner merges stdout+stderr, so both the JSON body and `gh: not found
// (HTTP 404)` appear in the captured output.
func TestAgentTaskStateNotFound(t *testing.T) {
	for _, out := range []string{
		`{"documentation_url":"https://docs.github.com/rest","message":"not found"}` + "\ngh: not found (HTTP 404)",
		`{"message":"Not Found"}`,
		"gh: Not Found (HTTP 404)",
	} {
		c := withRunner(func(ctx context.Context, workdir, name string, args ...string) (string, error) {
			return out, errExitCode1{}
		})
		_, err := c.AgentTaskState(context.Background(), "https://github.com/o/r", "abc-123")
		if !errors.Is(err, ErrTaskNotFound) {
			t.Fatalf("out=%q → want ErrTaskNotFound, got %v", out, err)
		}
	}
}

// TestAgentTaskStateTransient: a 5xx/timeout is NOT ErrTaskNotFound — the conductor
// keeps its backoff instead of declaring the session dead.
func TestAgentTaskStateTransient(t *testing.T) {
	c := withRunner(func(ctx context.Context, workdir, name string, args ...string) (string, error) {
		return `{"message":"Server Error"}` + "\ngh: (HTTP 503)", errExitCode1{}
	})
	_, err := c.AgentTaskState(context.Background(), "https://github.com/o/r", "abc-123")
	if err == nil {
		t.Fatal("want an error for a 503")
	}
	if errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("a 503 must NOT be ErrTaskNotFound: %v", err)
	}
}

// TestAgentTaskStateOK returns the live state on success.
func TestAgentTaskStateOK(t *testing.T) {
	c := withRunner(func(ctx context.Context, workdir, name string, args ...string) (string, error) {
		return "in_progress\n", nil
	})
	state, err := c.AgentTaskState(context.Background(), "https://github.com/o/r", "abc-123")
	if err != nil || state != "in_progress" {
		t.Fatalf("state=%q err=%v, want in_progress/nil", state, err)
	}
}

// TestWorkflowRunsActive: any non-terminal run → active; all terminal → inactive.
func TestWorkflowRunsActive(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"one in_progress", `{"workflow_runs":[{"status":"completed"},{"status":"in_progress"}]}`, true},
		{"queued", `{"workflow_runs":[{"status":"queued"}]}`, true},
		{"all terminal", `{"workflow_runs":[{"status":"completed"},{"status":"failure"},{"status":"cancelled"}]}`, false},
		{"none", `{"workflow_runs":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := withRunner(func(ctx context.Context, workdir, name string, args ...string) (string, error) {
				return tc.body, nil
			})
			got, err := c.WorkflowRunsActive(context.Background(), "https://github.com/o/r", "claude.yml")
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if got != tc.want {
				t.Fatalf("active=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestWorkflowRunsActiveFailsClosed: an API error returns active=true so the
// conductor never declares a possibly-live run dead on an inconclusive read.
func TestWorkflowRunsActiveFailsClosed(t *testing.T) {
	c := withRunner(func(ctx context.Context, workdir, name string, args ...string) (string, error) {
		return "boom", errExitCode1{}
	})
	got, err := c.WorkflowRunsActive(context.Background(), "https://github.com/o/r", "claude.yml")
	if err == nil {
		t.Fatal("want an error")
	}
	if !got {
		t.Fatal("fail-closed: want active=true on error")
	}
}

// TestRemoveIssueRunningIdempotent: a 404 (label already absent) is not an error.
func TestRemoveIssueRunningIdempotent(t *testing.T) {
	c := withRunner(func(ctx context.Context, workdir, name string, args ...string) (string, error) {
		return `{"message":"Label does not exist"}` + "\ngh: Not Found (HTTP 404)", errExitCode1{}
	})
	if err := c.RemoveIssueRunning(context.Background(), "https://github.com/o/r", []int{7}); err != nil {
		t.Fatalf("404 (label ausente) no debe ser error: %v", err)
	}
}
