package github

import (
	"context"
	"strings"
)

// Owners returns the authenticated identity's login, the accounts/orgs where the App is
// installed for them, plus the orgs it belongs to — the
// owners a repo can be created under. Uses THIS client's token (so a per-user client
// lists that user's orgs); a token-less client uses the host `gh` auth. Best-effort:
// a call that errors (missing scope, etc.) just contributes nothing.
func (c *Client) Owners(ctx context.Context) []string {
	seen := map[string]bool{}
	var out []string
	add := func(o string) {
		o = strings.TrimSpace(o)
		if o != "" && !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	if login, err := c.runner(ctx, "", "gh", "api", "user", "--jq", ".login"); err == nil {
		add(login)
	}
	// Accounts/orgs where THIS App is installed and the user has access — the authoritative
	// "where can I create repos" list. An org membership can be PRIVATE (absent from
	// user/orgs), but if the App is installed there the user can still target it. This is
	// what actually gates repo creation via the App, so it must be the primary source.
	if inst, err := c.runner(ctx, "", "gh", "api", "user/installations", "--paginate", "--jq", ".installations[].account.login"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(inst), "\n") {
			add(l)
		}
	}
	if orgs, err := c.runner(ctx, "", "gh", "api", "user/orgs", "--paginate", "--jq", ".[].login"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(orgs), "\n") {
			add(l)
		}
	}
	return out
}
