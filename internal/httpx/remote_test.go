package httpx_test

import (
	"testing"

	"vibeforge-kernel/internal/httpx"
)

func TestValidateRemote(t *testing.T) {
	t.Setenv("VIBEFORGE_TARGET_ALLOWLIST", "") // ensure no allowlist interference.

	allowed := []string{
		"https://github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"ssh://git@github.com/org/repo.git",
	}
	for _, r := range allowed {
		if err := httpx.ValidateRemote(r); err != nil {
			t.Errorf("allowed remote %q rejected: %v", r, err)
		}
	}

	blocked := []string{
		"ext::git upload-pack /etc/passwd",
		"file:///etc/passwd",
		"",
		"ftp://example.com/repo.git",
		"/local/path/repo.git",
	}
	for _, r := range blocked {
		if err := httpx.ValidateRemote(r); err == nil {
			t.Errorf("blocked remote %q was not rejected", r)
		}
	}
}

// TestValidateRemoteAllowlist: VIBEFORGE_TARGET_ALLOWLIST overrides the default
// scheme set — only configured prefixes are accepted.
func TestValidateRemoteAllowlist(t *testing.T) {
	t.Setenv("VIBEFORGE_TARGET_ALLOWLIST", "https://internal.corp/,git@internal:")

	if err := httpx.ValidateRemote("https://internal.corp/repo.git"); err != nil {
		t.Errorf("allowlist match rejected: %v", err)
	}
	if err := httpx.ValidateRemote("git@internal:repo.git"); err != nil {
		t.Errorf("allowlist match rejected: %v", err)
	}
	// A normally-allowed URL not in the allowlist is rejected when allowlist is set.
	if err := httpx.ValidateRemote("https://github.com/org/repo.git"); err == nil {
		t.Error("URL outside allowlist should have been rejected")
	}
}
