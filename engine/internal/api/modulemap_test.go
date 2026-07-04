package api

import (
	"strings"
	"testing"

	"forge/internal/tickets"
)

func TestRenderModuleMap(t *testing.T) {
	// Empty graph → empty string so WriteModuleMap writes no file.
	if s := renderModuleMap(nil); s != "" {
		t.Fatalf("empty map = %q, want empty", s)
	}
	mods := []tickets.ModuleHit{
		{Dir: "frontend/src", Files: 3, Lanes: []string{"react-dev"}, Stories: []string{"S1", "S4"}},
	}
	got := renderModuleMap(mods)
	for _, want := range []string{"# Module map", "`frontend/src/`", "react-dev", "S1, S4", "| 3 |"} {
		if !strings.Contains(got, want) {
			t.Fatalf("module map missing %q:\n%s", want, got)
		}
	}
}
