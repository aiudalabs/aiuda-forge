// Package settings is a small file-backed store for control-plane configuration
// the UI edits: MCP connections (JIRA/GitHub), agent auth, and sandbox. It is
// config — NOT execution — so it lives in the control plane next to the registry,
// not in the kernel. Secret-shaped fields are never returned in clear (Masked()).
//
// Execution settings (execution_unit, merge_mode) are NO LONGER global: they were
// moved to per-project storage (internal/projects, audit A2) so two projects can
// run under different modes concurrently. The dead merge_policy field (the
// `approval: risk-policy` factory step is a Wave-6 no-op — never wired) was
// removed entirely.
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrInvalid wraps validation failures from Put so callers (the HTTP layer) can
// distinguish a bad request from a server fault and answer 400 instead of 500.
var ErrInvalid = errors.New("invalid settings")

// Settings is the editable GLOBAL configuration surface: MCP, agent auth, and
// sandbox only. Per-project execution settings live in internal/projects.
type Settings struct {
	// MCP connections, e.g. {"github": {"connected": true, "repo": "nmlemus/x"}}.
	MCP map[string]map[string]any `json:"mcp"`
	// Agent auth: mode is subscription|api_key|oauth_token; Secret is the token
	// (stored, never returned in clear — Masked() replaces it with a sentinel).
	AgentAuth AgentAuth `json:"agent_auth"`
	// Sandbox runtime config (docker|local · gVisor · image · egress allowlist).
	Sandbox map[string]any `json:"sandbox"`
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
	return st, nil
}

func defaults() Settings {
	return Settings{
		MCP:       map[string]map[string]any{},
		AgentAuth: AgentAuth{Mode: "subscription"},
		Sandbox:   map[string]any{"runtime": "docker", "egress": "allowlist"},
	}
}

// Get returns the current settings with secrets MASKED — safe for any client.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s.Masked()
}

// secretMCPKeys are MCP connection fields treated as secrets (masked on read,
// preserved on a masked round-trip). Telegram/JIRA/etc tokens live here (v1.3).
var secretMCPKeys = map[string]bool{"token": true, "secret": true, "bot_token": true, "api_token": true, "api_key": true}

func isSecretKey(k string) bool { return secretMCPKeys[k] }

// MCPValue returns the RAW (unmasked) value of an MCP connection field, e.g. the
// Telegram bot token. For internal use (sending), never for client responses.
func (st *Store) MCPValue(connector, key string) string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	// Connection names are user-entered in the console (e.g. "Telegram" vs
	// "telegram"); match case-insensitively so a different capitalization still
	// resolves the connector's config.
	for name, conn := range st.s.MCP {
		if !strings.EqualFold(name, connector) {
			continue
		}
		if v, ok := conn[key].(string); ok {
			return v
		}
	}
	return ""
}

// Masked returns a copy where secret-shaped fields are replaced with a sentinel:
// AgentAuth.Secret and any secret-shaped MCP connection field. The MCP map is
// deep-copied so masking never mutates the stored settings.
func (s Settings) Masked() Settings {
	out := s
	if out.AgentAuth.Secret != "" {
		out.AgentAuth.Secret = secretMask
	}
	if s.MCP != nil {
		mcp := make(map[string]map[string]any, len(s.MCP))
		for name, conn := range s.MCP {
			cp := make(map[string]any, len(conn))
			for k, v := range conn {
				if isSecretKey(k) {
					if sv, ok := v.(string); ok && sv != "" {
						cp[k] = secretMask
						continue
					}
				}
				cp[k] = v
			}
			mcp[name] = cp
		}
		out.MCP = mcp
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
		// Preserve stored MCP secrets when the client sent the mask (or empty) — a
		// round-trip of GET→PUT must not wipe the real token (mirrors AgentAuth).
		for name, conn := range in.MCP {
			for k, v := range conn {
				if !isSecretKey(k) {
					continue
				}
				sv, ok := v.(string)
				if ok && sv != secretMask && sv != "" {
					continue // a real new secret was provided — keep it
				}
				if old, ok := st.s.MCP[name]; ok {
					if ov, ok := old[k].(string); ok && ov != "" {
						conn[k] = ov // restore the stored secret
					}
				}
			}
		}
		st.s.MCP = in.MCP
	}
	if in.Sandbox != nil {
		st.s.Sandbox = in.Sandbox
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
