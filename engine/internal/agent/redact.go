package agent

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Secret redaction for the live-log (audit C4). Every step.event (tool input /
// assistant text) is persisted and served via GET /runs/{id}/events; a
// prompt-injected agent that runs `echo $CLAUDE_CODE_OAUTH_TOKEN` would otherwise
// land the real token in the event store. We replace any token-shaped substring,
// and any explicitly-registered secret value, with the redaction marker BEFORE
// the row is persisted.

// redactionMarker replaces any matched secret.
const redactionMarker = "«redacted»"

// tokenPatterns match well-known credential shapes by prefix. Each captures the
// prefix plus a run of token characters so the whole secret is masked, not just
// the prefix. Anchored to a token-char class so surrounding prose is untouched.
var tokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`),              // Anthropic API keys
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),                  // GitHub personal access token
	regexp.MustCompile(`gho_[A-Za-z0-9]{20,}`),                  // GitHub OAuth token
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),          // GitHub fine-grained PAT
	regexp.MustCompile(`AKIA[A-Z0-9]{16}`),                      // AWS access key id
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]{16,}=*`), // Authorization: Bearer <token>
}

// dynamicSecrets are exact secret values registered at startup (the agent_auth
// token, VIBEFORGE_API_TOKEN). Guarded by a mutex; redaction reads a snapshot.
var (
	dynMu          sync.RWMutex
	dynamicSecrets []string
)

// RegisterSecret adds an exact secret value to redact from the live-log. Short
// or empty values are ignored (a 1-char "secret" would mangle all output). Safe
// to call repeatedly; duplicates are de-duped. Call this at startup with the
// configured agent auth token and VIBEFORGE_API_TOKEN.
func RegisterSecret(secret string) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 8 {
		return // too short to be a real secret; avoid over-redacting
	}
	dynMu.Lock()
	defer dynMu.Unlock()
	for _, s := range dynamicSecrets {
		if s == secret {
			return
		}
	}
	dynamicSecrets = append(dynamicSecrets, secret)
	// Longest-first so a secret that is a prefix of another is masked correctly.
	sort.Slice(dynamicSecrets, func(i, j int) bool {
		return len(dynamicSecrets[i]) > len(dynamicSecrets[j])
	})
}

// redactSecrets returns s with every known token shape and registered secret
// value replaced by the redaction marker. It is allocation-light for the common
// case (no secret present): the pattern/value scans short-circuit on no match.
func redactSecrets(s string) string {
	if s == "" {
		return s
	}
	// Exact registered secrets first (longest-first, set above).
	dynMu.RLock()
	for _, secret := range dynamicSecrets {
		if strings.Contains(s, secret) {
			s = strings.ReplaceAll(s, secret, redactionMarker)
		}
	}
	dynMu.RUnlock()
	// Then token-shaped patterns.
	for _, re := range tokenPatterns {
		if re.MatchString(s) {
			s = re.ReplaceAllString(s, redactionMarker)
		}
	}
	return s
}
