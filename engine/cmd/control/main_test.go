package main

import (
	"strings"
	"testing"
)

// The boot guard added after the 2026-07-09 incident: a with-users deployment must
// NOT start on a localhost/empty public URL (GitHub OAuth callbacks + preview links
// break), unless the operator explicitly opted into a local dev instance.

func TestIsLocalURL(t *testing.T) {
	cases := map[string]bool{
		"":                                true, // empty = not a public identity
		"   ":                             true, // blank
		"http://localhost:8080":           true,
		"http://127.0.0.1:8080":           true,
		"http://0.0.0.0:8080":             true,
		"http://[::1]:8080":               true,
		"https://LOCALHOST/forge-api":     true, // case-insensitive
		"https://fluxo.aiudalabs.com/forge-api": false,
		"https://forge.example.org":       false,
	}
	for url, want := range cases {
		if got := isLocalURL(url); got != want {
			t.Errorf("isLocalURL(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestLocalPublicURLFatal(t *testing.T) {
	const prod = "https://fluxo.aiudalabs.com/forge-api"

	// The bug scenario: real deployment (users exist), localhost URL, no opt-out → FATAL.
	if msg := localPublicURLFatal("http://localhost:8080", 1, false); msg == "" {
		t.Fatal("expected FATAL for localhost public URL with users, got none")
	} else {
		// Message must be actionable: name the var and the fix.
		for _, want := range []string{"VIBEFORGE_PUBLIC_URL", "COMPOSE_FILE", "VIBEFORGE_ALLOW_LOCAL_URL"} {
			if !strings.Contains(msg, want) {
				t.Errorf("fatal message missing %q; got: %s", want, msg)
			}
		}
	}

	// Empty URL with users → also FATAL (silent misconfig).
	if msg := localPublicURLFatal("", 3, false); msg == "" {
		t.Error("expected FATAL for empty public URL with users")
	}

	// Explicit dev opt-out → allowed even with users on localhost.
	if msg := localPublicURLFatal("http://localhost:8080", 1, true); msg != "" {
		t.Errorf("VIBEFORGE_ALLOW_LOCAL_URL=1 should permit localhost; got FATAL: %s", msg)
	}

	// No users yet (token-only / first boot) → allowed to be local.
	if msg := localPublicURLFatal("http://localhost:8080", 0, false); msg != "" {
		t.Errorf("no users should not fail; got FATAL: %s", msg)
	}

	// Correct prod config → never fatal, with or without users.
	if msg := localPublicURLFatal(prod, 5, false); msg != "" {
		t.Errorf("valid public URL should not fail; got FATAL: %s", msg)
	}
	if msg := localPublicURLFatal(prod, 0, false); msg != "" {
		t.Errorf("valid public URL (no users) should not fail; got FATAL: %s", msg)
	}
}
