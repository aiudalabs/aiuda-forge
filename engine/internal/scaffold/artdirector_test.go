package scaffold

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// uiVerifyStacks are the three github-native stacks whose ui-verify.yml carries the
// art-director visual-acceptance step. app_path is only meaningful for the flutter
// stack; passing it everywhere is harmless (unused vars are ignored).
var uiVerifyStacks = []string{"react-supabase", "python-fastapi-react", "aiuda-flutter-firebase"}

func renderUIVerify(t *testing.T, stack string, vars Vars) string {
	t.Helper()
	files, _, err := Render(realTemplates, stack, vars)
	if err != nil {
		t.Fatalf("Render(%s): %v", stack, err)
	}
	content, ok := pathsOf(files)[".github/workflows/ui-verify.yml"]
	if !ok {
		t.Fatalf("%s: ui-verify.yml not in rendered files", stack)
	}
	return content
}

// TestArtDirectorStepPresentAndGated proves each of the three ui-verify templates
// carries the art-director step, cloned from the claude-review pattern (same action +
// secret), gated by BOTH the render-time art_director flag AND the runtime screen_key.
func TestArtDirectorStepPresentAndGated(t *testing.T) {
	for _, stack := range uiVerifyStacks {
		t.Run(stack, func(t *testing.T) {
			c := renderUIVerify(t, stack, Vars{"project_name": "Acme", "app_path": "apps/customer", "art_director": "on"})

			// The step exists and is the claude-review pattern (same action + secret).
			for _, want := range []string{
				"Art-director — resolver screen_key del issue enlazado",
				"anthropics/claude-code-action@v1",
				"claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}",
				".art-director/verdict.txt", // deterministic FAIL=red-check gate
				"construye la pantalla",     // screen_key extraction from the issue
			} {
				if !strings.Contains(c, want) {
					t.Errorf("%s ui-verify missing %q", stack, want)
				}
			}
			// Gated by screen_key: the render/judge/verdict steps only run when a
			// screen_key resolved (empty = skip clean).
			if !strings.Contains(c, "steps.artdir.outputs.key != ''") {
				t.Errorf("%s: art-director steps must condition on the resolved screen_key", stack)
			}
			// Gated by the flag (on) — the resolve step reads `!= 'off'`.
			if !strings.Contains(c, "'on' != 'off'") {
				t.Errorf("%s: with art_director=on the step guard should read 'on' != 'off'", stack)
			}
			// The project name reaches the art-director prompt (same as claude-review).
			if !strings.Contains(c, "gate for Acme") {
				t.Errorf("%s: project_name should substitute into the art-director prompt", stack)
			}
		})
	}
}

// TestArtDirectorFlagOffAndOn proves the flag toggles the step's guard: off renders a
// present-but-skipped step ('off' != 'off' is false); on renders it active. Both render
// as valid content (the whole point of the {{art_director}} render-time flag: measure
// cost/value per project without editing the template).
func TestArtDirectorFlagOffAndOn(t *testing.T) {
	for _, stack := range uiVerifyStacks {
		t.Run(stack, func(t *testing.T) {
			off := renderUIVerify(t, stack, Vars{"project_name": "Acme", "app_path": "apps/customer", "art_director": "off"})
			if !strings.Contains(off, "'off' != 'off'") {
				t.Errorf("%s: art_director=off should render a guard that skips ('off' != 'off')", stack)
			}
			// The step is still PRESENT (rendering is toggling the guard, not removing
			// the block) — a clean skip at run time.
			if !strings.Contains(off, "anthropics/claude-code-action@v1") {
				t.Errorf("%s: the art-director step should still be present when off (it skips at run time)", stack)
			}
			on := renderUIVerify(t, stack, Vars{"project_name": "Acme", "app_path": "apps/customer", "art_director": "on"})
			if strings.Contains(on, "{{art_director}}") {
				t.Errorf("%s: {{art_director}} must be substituted when passed", stack)
			}
			// The rendered workflow must be valid YAML in both toggle states — the
			// nested single-quotes in the `if:` guards are the risk.
			for _, c := range []string{off, on} {
				var doc any
				if err := yaml.Unmarshal([]byte(c), &doc); err != nil {
					t.Fatalf("%s: rendered ui-verify.yml is not valid YAML: %v", stack, err)
				}
			}
		})
	}
}

// TestArtDirectorDefaultsOnWhenUnset documents the belt-and-suspenders default: if a
// caller forgets to pass art_director, the literal `{{art_director}}` survives and the
// guard `'{{art_director}}' != 'off'` is truthy → the step runs (default on). It is also
// reported in `missing` so the omission is visible.
func TestArtDirectorDefaultsOnWhenUnset(t *testing.T) {
	files, missing, err := Render(realTemplates, "react-supabase", Vars{"project_name": "Acme"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	c := pathsOf(files)[".github/workflows/ui-verify.yml"]
	if !strings.Contains(c, "'{{art_director}}' != 'off'") {
		t.Errorf("unset art_director should leave a truthy (default-on) guard, got no literal guard")
	}
	if !contains(missing, "art_director") {
		t.Errorf("unset art_director should be reported in missing; got %v", missing)
	}
}
