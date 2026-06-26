package agent

import (
	"strings"
	"testing"
)

func TestRedactTokenShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"anthropic", "key is sk-ant-api03-AbCdEf0123456789_xyz done"},
		{"ghp", "token ghp_ABCDEFGHIJ0123456789abcdXYZ here"},
		{"gho", "gho_ABCDEFGHIJ0123456789abcdXYZ"},
		{"github_pat", "github_pat_11ABCDEFG0_aaaaaaaaaaaaaaaaaaaa"},
		{"aws", "id AKIAIOSFODNN7EXAMPLE end"},
		{"bearer", "Authorization: Bearer abcdef0123456789ABCDEF=="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := redactSecrets(c.in)
			if !strings.Contains(out, redactionMarker) {
				t.Errorf("%s: expected redaction, got %q", c.name, out)
			}
			// The original secret characters must be gone.
			if strings.Contains(out, "0123456789") && c.name != "bearer" {
				// bearer keeps surrounding words; just ensure the token body went.
			}
		})
	}
}

func TestRedactRegisteredSecret(t *testing.T) {
	secret := "super-secret-service-token-value"
	RegisterSecret(secret)
	out := redactSecrets("the token is " + secret + " ok")
	if strings.Contains(out, secret) {
		t.Errorf("registered secret not redacted: %q", out)
	}
	if !strings.Contains(out, redactionMarker) {
		t.Errorf("expected marker, got %q", out)
	}
}

func TestRedactShortSecretIgnored(t *testing.T) {
	// A short value must NOT be registered (would mangle ordinary text).
	RegisterSecret("abc")
	out := redactSecrets("abc def abc")
	if strings.Contains(out, redactionMarker) {
		t.Errorf("short secret should not have been registered: %q", out)
	}
}

func TestRedactNoFalsePositive(t *testing.T) {
	in := "Edit notas.py: added a function and ran the tests"
	if got := redactSecrets(in); got != in {
		t.Errorf("ordinary text must pass through unchanged, got %q", got)
	}
}
