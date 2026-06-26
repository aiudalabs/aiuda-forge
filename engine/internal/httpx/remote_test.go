package httpx_test

import (
	"testing"

	"forge/internal/httpx"
)

func TestValidateRemoteAllowed(t *testing.T) {
	t.Setenv("VIBEFORGE_REPO_ALLOWLIST", "") // default allowlist (github.com)

	allowed := []string{
		"https://github.com/org/repo.git",
		"https://github.com/org/repo",
		"https://github.com:443/org/repo.git",
	}
	for _, r := range allowed {
		if err := httpx.ValidateRemote(r); err != nil {
			t.Errorf("allowed remote %q rejected: %v", r, err)
		}
	}
}

func TestValidateRemoteBlocked(t *testing.T) {
	t.Setenv("VIBEFORGE_REPO_ALLOWLIST", "")

	blocked := map[string]string{
		"":                                         "empty",
		"http://github.com/org/repo.git":           "http scheme",
		"git@github.com:org/repo.git":              "scp-style / no https",
		"ssh://git@github.com/org/repo.git":        "ssh scheme",
		"ext::git upload-pack /etc/passwd":         "ext:: command exec",
		"file:///etc/passwd":                       "file scheme",
		"https://github.com.evil.com/org/repo.git": "suffix-attached lookalike host",
		"https://evilgithub.com/org/repo.git":      "non-allowlisted host",
		"https://oauth2:TOKEN@github.com/org/repo": "credentials in URL",
		"https://169.254.169.254/latest/meta-data": "cloud metadata IP",
		"https://127.0.0.1/repo.git":               "loopback",
		"https://10.0.0.1/repo.git":                "private 10/8",
		"https://172.16.0.1/repo.git":              "private 172.16/12",
		"https://192.168.1.1/repo.git":             "private 192.168/16",
		"https://localhost/repo.git":               "localhost",
		"https://[::1]/repo.git":                   "ipv6 loopback",
	}
	for r, why := range blocked {
		if err := httpx.ValidateRemote(r); err == nil {
			t.Errorf("blocked remote %q (%s) was not rejected", r, why)
		}
	}
}

// TestValidateRemoteAllowlist: VIBEFORGE_REPO_ALLOWLIST replaces the default —
// exact host match only.
func TestValidateRemoteAllowlist(t *testing.T) {
	t.Setenv("VIBEFORGE_REPO_ALLOWLIST", "git.internal.corp, gitlab.com")

	if err := httpx.ValidateRemote("https://gitlab.com/org/repo.git"); err != nil {
		t.Errorf("allowlist match rejected: %v", err)
	}
	if err := httpx.ValidateRemote("https://git.internal.corp/org/repo.git"); err != nil {
		t.Errorf("allowlist match (trimmed) rejected: %v", err)
	}
	// github.com is NOT in the custom allowlist → rejected.
	if err := httpx.ValidateRemote("https://github.com/org/repo.git"); err == nil {
		t.Error("github.com should be rejected when a custom allowlist is set")
	}
	// Exact-match: a subdomain of an allowlisted host is NOT allowed.
	if err := httpx.ValidateRemote("https://www.gitlab.com/org/repo.git"); err == nil {
		t.Error("subdomain of allowlisted host should be rejected (exact match)")
	}
}
