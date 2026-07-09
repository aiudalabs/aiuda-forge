package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestArtDirectorPersonaAndSkillLoad proves the art-director persona + the
// visual-acceptance skill parse from the real registry and wire together: the manifest
// declares a (vision-capable) model, the persona markdown loads, and the skill is
// injected into the persona as the loader does for any declared skill.
func TestArtDirectorPersonaAndSkillLoad(t *testing.T) {
	loader := NewDirLoader(filepath.Join("..", "..", "registry", "agents"))
	ad, err := loader.Load("art-director")
	if err != nil {
		t.Fatalf("load art-director: %v", err)
	}
	if ad.Model == "" {
		t.Fatalf("art-director must set a (vision) model, got %+v", ad)
	}
	if ad.Persona == "" {
		t.Fatal("art-director persona (.md) missing")
	}
	// The declared visual-acceptance skill is injected into the persona.
	if !strings.Contains(ad.Persona, "Skill: visual-acceptance") {
		t.Errorf("visual-acceptance skill must be injected into the art-director persona")
	}
	// And the skill carries the verdict contract the ui-verify workflow depends on.
	for _, want := range []string{"VERDICT: PASS", "VERDICT: FAIL"} {
		if !strings.Contains(ad.Persona, want) {
			t.Errorf("visual-acceptance skill must define %q", want)
		}
	}
}
