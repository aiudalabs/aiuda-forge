package scaffold

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Regresión del gate de review rojo en TODO PR de agente: claude-review corre en
// `pull_request`, y los PRs los abre un BOT (claude_action → `claude`; copilot →
// `copilot-swe-agent`). claude-code-action rechaza actores no-humanos por defecto
// ("Workflow initiated by non-human actor: claude (type: Bot)") salvo que se pase
// `allowed_bots`. El gate es read-only (no edita código) → revisar PRs de cualquier
// bot es lo correcto. El test rinde el template real y verifica el input, no el
// texto crudo del .tmpl.
func TestScaffoldedClaudeReviewAllowsBots(t *testing.T) {
	vars := Vars{
		"project_name": "Acme",
		"language":     "es",
		"art_director": "on",
		"app_path":     "frontend",
	}
	// claude-review vive en _common → se rinde igual para todos los stacks; con uno basta.
	files, _, err := Render(realTemplates, "python-fastapi-react", vars)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	type wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string                 `yaml:"uses"`
				With map[string]interface{} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}

	seen := false
	for _, f := range files {
		if !strings.Contains(f.Path, ".github/workflows/claude-review") {
			continue
		}
		seen = true
		var w wf
		if err := yaml.Unmarshal([]byte(f.Content), &w); err != nil {
			t.Fatalf("%s: YAML rendido no parsea: %v", f.Path, err)
		}
		foundStep := false
		for name, job := range w.Jobs {
			for _, s := range job.Steps {
				if !strings.Contains(s.Uses, "claude-code-action") {
					continue
				}
				foundStep = true
				ab, ok := s.With["allowed_bots"]
				if !ok || strings.TrimSpace(toStr(ab)) == "" {
					t.Errorf("%s job %q: claude-code-action sin `allowed_bots` → el review rechaza los PRs de agentes (bots) con \"non-human actor\"", f.Path, name)
				}
			}
		}
		if !foundStep {
			t.Errorf("%s: no se encontró el step claude-code-action — ¿cambió el workflow?", f.Path)
		}
	}
	if !seen {
		t.Fatal("no se renderizó ningún workflow claude-review — el test no verificó nada")
	}
}

func toStr(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
