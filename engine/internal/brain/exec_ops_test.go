package brain

import "testing"

func toolNames() map[string]bool {
	m := map[string]bool{}
	for _, t := range toolList() {
		m[t.Name] = true
	}
	return m
}

func TestExecOptInGate(t *testing.T) {
	// Off by default: not offered to the LLM, and toolEnabled refuses it.
	if toolEnabled("exec") {
		t.Fatal("exec must be disabled without VIBEFORGE_BRAIN_EXEC")
	}
	if toolNames()["exec"] {
		t.Fatal("exec must NOT appear in toolList when disabled")
	}

	// Opt-in flips it on.
	t.Setenv("VIBEFORGE_BRAIN_EXEC", "1")
	if !toolEnabled("exec") {
		t.Fatal("exec must be enabled with VIBEFORGE_BRAIN_EXEC=1")
	}
	if !toolNames()["exec"] {
		t.Fatal("exec must appear in toolList when enabled")
	}
}

func TestExecIsMutatingOwnerOnly(t *testing.T) {
	d, err := lookup("exec")
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != Mutating {
		t.Errorf("exec must be Mutating (human-approved), got %s", d.Kind)
	}
	if d.MinRole != "owner" {
		t.Errorf("exec must be owner-only, got %s", d.MinRole)
	}
}
