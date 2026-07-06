package github

import (
	"context"
	"testing"
)

// TestListCommitsForPath parses the gh-api commits projection into FileCommit history.
func TestListCommitsForPath(t *testing.T) {
	c := &Client{runner: func(_ context.Context, _ string, name string, args ...string) (string, error) {
		return `[{"sha":"abc123","date":"2026-07-06T12:00:00Z","message":"design: publish docs/PRD.md (prd_gate approved)"},{"sha":"def456","date":"2026-07-05T10:00:00Z","message":"design: initial"}]`, nil
	}}
	got, err := c.ListCommitsForPath(context.Background(), "https://github.com/acme/r", "design", "docs/PRD.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 commits, got %d", len(got))
	}
	if got[0].SHA != "abc123" || got[0].Date == "" || got[0].Message == "" {
		t.Fatalf("bad first commit: %+v", got[0])
	}
}

// TestListCommitsForPathEmpty: a 404 (branch/path absent) yields empty, not an error.
func TestListCommitsForPathEmpty(t *testing.T) {
	c := &Client{runner: func(_ context.Context, _ string, _ string, _ ...string) (string, error) {
		return "gh: Not Found (HTTP 404)", context.DeadlineExceeded // any error + a 404-ish body
	}}
	got, err := c.ListCommitsForPath(context.Background(), "https://github.com/acme/r", "design", "docs/NEW.md")
	if err != nil {
		t.Fatalf("404 should be empty, not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}
