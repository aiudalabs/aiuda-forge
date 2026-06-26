package httpx

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// defaultRepoAllowlist is the host(s) a repo URL may point at when no
// VIBEFORGE_REPO_ALLOWLIST is configured. Cloning runs untrusted code, so the
// default is deliberately a single, well-known host.
var defaultRepoAllowlist = []string{"github.com"}

// ValidateRemote checks that remote is a safe git URL before cloning (audit
// C3/C5). It PARSES the URL (never prefix-matches) and enforces:
//
//   - scheme must be https (http, git, ssh, ext::, file:// are all rejected —
//     they are SSRF/cleartext/command-execution vectors);
//   - NO userinfo (credentials embedded in the URL are rejected outright);
//   - host must EXACTLY match an allowlisted host (so "github.com.evil.com"
//     fails — a prefix/suffix check would not);
//   - the host must not be (or resolve syntactically to) an internal,
//     link-local, loopback or cloud-metadata address.
//
// The allowlist is "github.com" plus any comma-separated hosts in
// VIBEFORGE_REPO_ALLOWLIST. Returns a non-nil error for any disallowed URL.
func ValidateRemote(remote string) error {
	if strings.TrimSpace(remote) == "" {
		return fmt.Errorf("remote is empty")
	}

	u, err := url.Parse(remote)
	if err != nil {
		return fmt.Errorf("remote %q is not a valid URL: %w", remote, err)
	}

	// Scheme: https only. This blocks ext::/file:// (command-exec), git:// and
	// http:// (cleartext / no host auth), and ssh:// (key-based, not validated here).
	if u.Scheme != "https" {
		return fmt.Errorf("remote %q must use https (got scheme %q)", remote, u.Scheme)
	}

	// Userinfo: reject creds-in-URL (https://oauth2:TOKEN@github.com/...).
	if u.User != nil {
		return fmt.Errorf("remote %q embeds credentials in the URL (userinfo not allowed)", remote)
	}

	host := u.Hostname() // strips any :port, lower-cased by url for registered names
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return fmt.Errorf("remote %q has no host", remote)
	}

	// Block internal / link-local / loopback / metadata targets (SSRF).
	if isBlockedHost(host) {
		return fmt.Errorf("remote %q targets a blocked internal/metadata host", remote)
	}

	// Exact host match against the allowlist.
	for _, allowed := range repoAllowlist() {
		if host == allowed {
			return nil
		}
	}
	return fmt.Errorf("remote %q host %q is not in the allowlist", remote, host)
}

// repoAllowlist returns the configured allowlist (exact hosts). When
// VIBEFORGE_REPO_ALLOWLIST is set it REPLACES the default; entries are trimmed
// and lower-cased.
func repoAllowlist() []string {
	if v := os.Getenv("VIBEFORGE_REPO_ALLOWLIST"); strings.TrimSpace(v) != "" {
		var out []string
		for _, h := range strings.Split(v, ",") {
			h = strings.ToLower(strings.TrimSpace(h))
			if h != "" {
				out = append(out, h)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return defaultRepoAllowlist
}

// isBlockedHost reports whether host is an internal/link-local/loopback/metadata
// target. It blocks the literal "localhost", any IP literal in a private/
// loopback/link-local range (covering the cloud-metadata 169.254.169.254), and
// the metadata hostname.
func isBlockedHost(host string) bool {
	switch host {
	case "localhost", "metadata", "metadata.google.internal":
		return true
	}
	// Strip IPv6 brackets if present.
	h := strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if ip := net.ParseIP(h); ip != nil {
		return isBlockedIP(ip)
	}
	return false
}

// isBlockedIP reports whether ip is loopback, private, link-local (which
// includes 169.254.169.254 cloud metadata), unspecified, or in the IPv4
// CGNAT/private ranges the audit calls out (127/8, 10/8, 172.16/12, 192.168/16,
// 169.254/16, ::1).
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	return false
}
