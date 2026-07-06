package github

import (
	"context"
	"strings"
)

// Owners returns the authenticated identity's login plus the orgs it belongs to — the
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
	if orgs, err := c.runner(ctx, "", "gh", "api", "user/orgs", "--paginate", "--jq", ".[].login"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(orgs), "\n") {
			add(l)
		}
	}
	return out
}
