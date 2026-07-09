package method

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"forge/internal/workflow"
	"gopkg.in/yaml.v3"
)

// Guardrail error sentinels — distinct so tests can assert the exact rejection reason.
var (
	errInvalid     = errors.New("invalid proposal")
	errProtected   = errors.New("protected method file")
	errUnparseable = errors.New("unparseable content")
)

var yamlFenceRe = regexp.MustCompile("(?s)```(?:ya?ml)?\\s*\\n(.*?)```")

// readProposals reads a RETRO doc and extracts its `proposals:` list. Like plan_apply's
// action parser, it prefers a fenced yaml block that carries a `proposals:` key, then
// falls back to parsing the whole file as yaml. A doc with NO `proposals:` key at all is
// an intentional no-op (an empty retro): it returns an empty slice, not an error, so the
// apply stamps retro_at and writes nothing.
func readProposals(path string) ([]Proposal, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseProposalsBytes(raw)
}

// parseProposalsBytes is the SINGLE proposals parser, shared by registry_apply
// (via readProposals) and the `validate` step type (via ParseProposals) so the two
// never diverge. It prefers a fenced yaml block carrying a `proposals:` key, then
// falls back to the whole doc. No proposals block at all → empty slice (an empty
// retro is legal, not an error).
func parseProposalsBytes(raw []byte) ([]Proposal, error) {
	for _, m := range yamlFenceRe.FindAllSubmatch(raw, -1) {
		block := m[1]
		if !containsProposalsKey(block) {
			continue
		}
		var doc proposalDoc
		if err := yaml.Unmarshal(block, &doc); err != nil {
			return nil, fmt.Errorf("proposals block: %w", err)
		}
		return doc.Proposals, nil
	}
	if containsProposalsKey(raw) {
		var doc proposalDoc
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("proposals: %w", err)
		}
		return doc.Proposals, nil
	}
	return nil, nil // no proposals block → empty retro
}

// ParseProposals is the exported bytes entry point to the shared proposals parser,
// for the `validate` step type to lint a RETRO doc without a store. Same code as
// registry_apply's readProposals — one parser.
func ParseProposals(raw []byte) ([]Proposal, error) { return parseProposalsBytes(raw) }

// containsProposalsKey reports whether a yaml chunk declares a `proposals:` key at the
// start of a line (so prose mentioning "proposals" does not false-positive).
func containsProposalsKey(b []byte) bool {
	for _, line := range splitLines(string(b)) {
		if hasProposalsPrefix(line) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func hasProposalsPrefix(line string) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return len(line)-i >= len("proposals:") && line[i:i+len("proposals:")] == "proposals:"
}

func failResult(detail string) workflow.StepResult {
	return workflow.StepResult{Success: false, Detail: detail}
}

// asString renders a plumbed input as a string (mirrors the tickets runners' helper).
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
