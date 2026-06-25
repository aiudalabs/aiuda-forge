// Package settings is a small file-backed store for control-plane configuration
// the UI edits: MCP connections (JIRA/GitHub), agent auth, sandbox, and the
// merge policy by risk. It is config — NOT execution — so it lives in the
// control plane next to the registry, not in the kernel. Secret-shaped fields
// are never returned in clear (Masked()).
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrInvalid wraps validation failures from Put so callers (the HTTP layer) can
// distinguish a bad request from a server fault and answer 400 instead of 500.
var ErrInvalid = errors.New("invalid settings")

// Settings is the editable configuration surface (doc 16 §2.7).
type Settings struct {
	// MCP connections, e.g. {"github": {"connected": true, "repo": "nmlemus/x"}}.
	MCP map[string]map[string]any `json:"mcp"`
	// Agent auth: mode is subscription|api_key|oauth_token; Secret is the token
	// (stored, never returned in clear — Masked() replaces it with a sentinel).
	AgentAuth AgentAuth `json:"agent_auth"`
	// Sandbox runtime config (docker|local · gVisor · image · egress allowlist).
	Sandbox map[string]any `json:"sandbox"`
	// Merge policy by lane/risk: {"low":"automerge","auth":"human_gate"}.
	MergePolicy map[string]string `json:"merge_policy"`
	// ExecutionUnit selects how the orchestrator batches work into runs:
	//   "sprint" (default) — a whole sprint is implemented in ONE run → ONE PR.
	//   "story"            — one run/PR per story (the original per-story behavior).
	ExecutionUnit string `json:"execution_unit"`
}

// Execution-unit modes. "sprint" is the default (goal mode).
const (
	ExecutionUnitSprint = "sprint"
	ExecutionUnitStory  = "story"
)

// validExecutionUnit reports whether v is an accepted execution_unit value.
func validExecutionUnit(v string) bool {
	return v == ExecutionUnitSprint || v == ExecutionUnitStory
}

// AgentAuth holds how the agent authenticates. Secret never crosses to clients.
type AgentAuth struct {
	Mode   string `json:"mode"`
	Secret string `json:"secret,omitempty"`
}

const secretMask = "••••••••" // shown instead of a stored secret

// Store is a thread-safe, file-backed Settings store.
type Store struct {
	path string
	mu   sync.RWMutex
	s    Settings
}

// Open loads (or initializes) settings at path.
func Open(path string) (*Store, error) {
	st := &Store{path: path, s: defaults()}
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, &st.s)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// Backfill for settings files written before execution_unit existed: an empty
	// value means "unset" → default to sprint (goal mode).
	if st.s.ExecutionUnit == "" {
		st.s.ExecutionUnit = ExecutionUnitSprint
	}
	return st, nil
}

func defaults() Settings {
	return Settings{
		MCP:           map[string]map[string]any{},
		AgentAuth:     AgentAuth{Mode: "subscription"},
		Sandbox:       map[string]any{"runtime": "docker", "egress": "allowlist"},
		MergePolicy:   map[string]string{"low": "automerge", "high": "human_gate"},
		ExecutionUnit: ExecutionUnitSprint, // default: goal mode (whole sprint per run)
	}
}

// Get returns the current settings with secrets MASKED — safe for any client.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s.Masked()
}

// Masked returns a copy where secret-shaped fields are replaced with a sentinel.
func (s Settings) Masked() Settings {
	out := s
	if out.AgentAuth.Secret != "" {
		out.AgentAuth.Secret = secretMask
	}
	return out
}

// Put merges an incoming Settings into the stored one and persists it. A masked
// or empty secret is treated as "unchanged" so a client round-trip never wipes
// the real token. Returns the masked result.
func (st *Store) Put(in Settings) (Settings, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if in.MCP != nil {
		st.s.MCP = in.MCP
	}
	if in.Sandbox != nil {
		st.s.Sandbox = in.Sandbox
	}
	if in.MergePolicy != nil {
		st.s.MergePolicy = in.MergePolicy
	}
	// execution_unit: an empty value means "unchanged"; a present value must be
	// one of the accepted modes — reject anything else so the orchestrator never
	// reads a garbage mode.
	if in.ExecutionUnit != "" {
		if !validExecutionUnit(in.ExecutionUnit) {
			return Settings{}, fmt.Errorf("%w: execution_unit %q must be %q or %q",
				ErrInvalid, in.ExecutionUnit, ExecutionUnitSprint, ExecutionUnitStory)
		}
		st.s.ExecutionUnit = in.ExecutionUnit
	}
	if in.AgentAuth.Mode != "" {
		st.s.AgentAuth.Mode = in.AgentAuth.Mode
	}
	// Only overwrite the secret if a real, non-masked one was provided.
	if in.AgentAuth.Secret != "" && in.AgentAuth.Secret != secretMask {
		st.s.AgentAuth.Secret = in.AgentAuth.Secret
	}
	if err := st.save(); err != nil {
		return Settings{}, err
	}
	return st.s.Masked(), nil
}

func (st *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(st.path, b, 0o600)
}
