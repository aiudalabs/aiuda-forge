package github

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// hasPutCall reports whether any recorded call was a `gh api -X PUT …` (the
// contents-API write WriteFile issues only when the file must be created/updated).
func hasPutCall(calls []call) bool {
	for _, c := range calls {
		if c.name == "gh" && len(c.args) >= 3 && c.args[0] == "api" && c.args[1] == "-X" && c.args[2] == "PUT" {
			return true
		}
	}
	return false
}

// TestWriteFileCreatesNew: a missing file (no sha) is created via a single PUT.
func TestWriteFileCreatesNew(t *testing.T) {
	var calls []call
	// No reply for the sha fetch → runner returns "" (file absent) → create path.
	c := withRunner(fakeRunner(map[string]string{}, &calls))

	changed, err := c.WriteFile(context.Background(),
		"https://github.com/acme/r", "dev", "docs/PRD.md", "hello", "msg")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true for a new file")
	}
	if !hasPutCall(calls) {
		t.Fatalf("expected a PUT call, calls=%v", calls)
	}
}

// TestWriteFileSkipsUnchanged: when the file already holds identical content, no PUT
// is issued (no empty commit / 422).
func TestWriteFileSkipsUnchanged(t *testing.T) {
	var calls []call
	replies := map[string]string{
		"gh api repos/acme/r/contents/docs/PRD.md?ref=dev --jq .sha":     "abc123\n",
		"gh api repos/acme/r/contents/docs/PRD.md?ref=dev --jq .content": base64.StdEncoding.EncodeToString([]byte("hello")),
	}
	c := withRunner(fakeRunner(replies, &calls))

	changed, err := c.WriteFile(context.Background(),
		"https://github.com/acme/r", "dev", "docs/PRD.md", "hello", "msg")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false for identical content")
	}
	if hasPutCall(calls) {
		t.Fatal("must not PUT when content is unchanged")
	}
}

// TestWriteFileUpdatesChanged: an existing file with different content is updated,
// and the request body carries the prior sha (so GitHub updates in place).
func TestWriteFileUpdatesChanged(t *testing.T) {
	var calls []call
	replies := map[string]string{
		"gh api repos/acme/r/contents/docs/PRD.md?ref=dev --jq .sha":     "abc123\n",
		"gh api repos/acme/r/contents/docs/PRD.md?ref=dev --jq .content": base64.StdEncoding.EncodeToString([]byte("OLD")),
	}
	c := withRunner(fakeRunner(replies, &calls))

	changed, err := c.WriteFile(context.Background(),
		"https://github.com/acme/r", "dev", "docs/PRD.md", "NEW", "msg")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when content differs")
	}
	if !hasPutCall(calls) {
		t.Fatalf("expected a PUT call, calls=%v", calls)
	}
	// The PUT must reference the prior sha in its JSON body (read back from --input).
	if !strings.Contains(strings.Join(flattenArgs(calls), " "), "--input") {
		t.Fatal("PUT should pass the body via --input")
	}
}

func flattenArgs(calls []call) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.args...)
	}
	return out
}
