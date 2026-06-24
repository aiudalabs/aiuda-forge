package httpx

import (
	"fmt"
	"os"
	"strings"
)

// ValidateRemote checks that remote is a safe git URL before cloning. Only
// https://, git@, and ssh:// are allowed; ext:: and file:// are git-protocol
// attack vectors that can execute arbitrary commands. If VIBEFORGE_TARGET_ALLOWLIST
// is set (comma-separated prefixes), any URL matching one of those prefixes is also
// accepted.
//
// Returns a non-nil error for any disallowed URL. Callers should treat that error
// as fatal (log.Fatalf) — an unsafe remote in the environment is a misconfiguration,
// not a recoverable runtime condition.
func ValidateRemote(remote string) error {
	if remote == "" {
		return fmt.Errorf("remote is empty")
	}

	// Explicit allowlist prefix overrides (operator-configured).
	if allow := os.Getenv("VIBEFORGE_TARGET_ALLOWLIST"); allow != "" {
		for _, prefix := range strings.Split(allow, ",") {
			prefix = strings.TrimSpace(prefix)
			if prefix != "" && strings.HasPrefix(remote, prefix) {
				return nil
			}
		}
		// Allowlist is set but nothing matched — reject.
		return fmt.Errorf("remote %q not in VIBEFORGE_TARGET_ALLOWLIST", remote)
	}

	// Default safe scheme set: https, git@host:, ssh://.
	if strings.HasPrefix(remote, "https://") ||
		strings.HasPrefix(remote, "git@") ||
		strings.HasPrefix(remote, "ssh://") {
		return nil
	}

	// Explicit block list for command-execution vectors.
	if strings.HasPrefix(remote, "ext::") || strings.HasPrefix(remote, "file://") {
		return fmt.Errorf("remote %q uses a disallowed scheme (ext:: and file:// are blocked)", remote)
	}

	return fmt.Errorf("remote %q uses an unsupported scheme; only https://, git@, and ssh:// are allowed", remote)
}
