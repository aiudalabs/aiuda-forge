package agent

import (
	"os"

	"forge/internal/sandbox"
)

// SandboxSentinelAPIKey is a NON-SECRET placeholder. The claude CLI requires
// ANTHROPIC_API_KEY to exist; in api_key mode the egress-proxy overwrites the
// x-api-key header with the REAL credential (which lives OUTSIDE the sandbox), so
// the container only ever sees this sentinel. Port of v1's SANDBOX_SENTINEL.
const SandboxSentinelAPIKey = "vibeforge-sandbox-sentinel-not-a-secret"

// forbiddenInSandbox are env vars that must NEVER reach the agent container. The
// allowlist is built positively (EgressEnv adds only what's needed), so this is
// the belt-and-suspenders the secrets test pins.
var forbiddenInSandbox = map[string]bool{
	"GH_TOKEN":                true,
	"VIBEFORGE_DAEMON_SECRET": true,
	"AWS_SECRET_ACCESS_KEY":   true,
	"DB_PASSWORD":             true,
	"ANTHROPIC_API_KEY":       true, // the REAL key never crosses; the sentinel is set explicitly below
	"ANTHROPIC_AUTH_TOKEN":    true,
}

// EgressConfig parameterizes how the agent reaches the LLM from inside the
// sandbox (defaults match v1 / the egress-proxy deploy).
type EgressConfig struct {
	ProxyURL      string // forward proxy with allowlist (HTTPS_PROXY); "" -> default
	AnthropicBase string // api_key mode reverse-proxy base; "" -> default
	Open          bool   // escape hatch: skip the proxy (direct egress) — residual risk
}

func (e EgressConfig) proxy() string {
	if e.ProxyURL != "" {
		return e.ProxyURL
	}
	return "http://egress-proxy:8888"
}

func (e EgressConfig) anthropicBase() string {
	if e.AnthropicBase != "" {
		return e.AnthropicBase
	}
	return "http://egress-proxy:8080/anthropic"
}

// EgressEnv builds the env injected into the agent CONTAINER (the -e flags),
// branched by auth mode (port of v1's egress_env + build_sandbox_env):
//
//   - api_key (strict): ANTHROPIC_BASE_URL -> egress-proxy + ANTHROPIC_API_KEY =
//     sentinel (the proxy injects the real key). Zero real-token exposure.
//   - subscription/oauth (passthrough): CLAUDE_CODE_OAUTH_TOKEN injected; claude
//     talks directly to api.anthropic.com (still THROUGH the allowlist proxy).
//
// In both modes HTTPS_PROXY/HTTP_PROXY point at the allowlist forward-proxy
// (unless Open). No daemon secret is ever added. This is the DOCKER container env.
func EgressEnv(auth Auth, cfg EgressConfig) []string {
	noProxy := "localhost,127.0.0.1"
	env := map[string]string{"NO_PROXY": noProxy, "no_proxy": noProxy}

	switch auth.Mode {
	case AuthAPIKey:
		env["ANTHROPIC_BASE_URL"] = cfg.anthropicBase()
		env["ANTHROPIC_API_KEY"] = SandboxSentinelAPIKey // sentinel, not a secret
	default: // subscription / oauth_token -> passthrough
		if auth.Token != "" {
			env["CLAUDE_CODE_OAUTH_TOKEN"] = auth.Token
		}
	}
	if !cfg.Open {
		p := cfg.proxy()
		env["HTTPS_PROXY"], env["HTTP_PROXY"] = p, p
		env["https_proxy"], env["http_proxy"] = p, p
	}
	// EgressEnv's own output is trusted (it never reads daemon secrets), so the
	// sentinel ANTHROPIC_API_KEY it sets in api_key mode is preserved. The
	// forbidden guard applies only when MERGING untrusted task vars (MergeAllowed).
	return toKV(env)
}

// MergeAllowed merges extra task-provided KEY=VALUE vars into base, DROPPING any
// forbidden (daemon-secret-shaped) key — the I3 guarantee for env_extra. The
// agent does not currently receive arbitrary task env, but this is the safe door
// if it ever does.
func MergeAllowed(base []string, extra map[string]string) []string {
	out := append([]string{}, base...)
	for k, v := range extra {
		if forbiddenInSandbox[k] || v == "" {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

// localAgentEnv is the env for the LOCAL fallback (agent runs on the host). It
// scrubs daemon secrets (DefaultAllowEnv only) but keeps HOME/PATH so the claude
// CLI works (subscription reads ~/.claude). NOT a security boundary, but it still
// never leaks GH_TOKEN/DB_* even locally.
func localAgentEnv(auth Auth) []string {
	base := sandbox.FilterEnv(os.Environ(), sandbox.DefaultAllowEnv)
	switch auth.Mode {
	case AuthAPIKey:
		base = append(base, "ANTHROPIC_API_KEY="+auth.Token)
	case AuthOAuthToken:
		base = append(base, "CLAUDE_CODE_OAUTH_TOKEN="+auth.Token)
	case AuthSubscription, "":
		// uses ~/.claude via HOME (kept by DefaultAllowEnv)
	}
	return base
}

// toKV renders a map to KEY=VALUE.
func toKV(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
