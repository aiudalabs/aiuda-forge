package scaffold

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Regresión del fallo "Could not fetch an OIDC token": todo workflow scaffoldeado que
// invoca anthropics/claude-code-action necesita `id-token: write` en el bloque de
// permissions EFECTIVO del job que la corre. Regla de GitHub: un `permissions:` a nivel
// JOB REEMPLAZA por completo al de workflow — si el job declara el suyo, id-token debe
// estar ahí; si no, aplica el de workflow. El test rinde los templates reales (post
// substitución) y verifica el permiso efectivo por job, no el texto crudo del .tmpl.

// ghWorkflow captura solo lo que importa para el chequeo de permisos.
type ghWorkflow struct {
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		// nil => el job NO declaró permissions (hereda el de workflow);
		// non-nil => el job los REEMPLAZA (regla de GitHub).
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Uses string `yaml:"uses"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func jobUsesClaudeAction(steps []struct {
	Uses string `yaml:"uses"`
}) bool {
	for _, s := range steps {
		if strings.Contains(s.Uses, "claude-code-action") {
			return true
		}
	}
	return false
}

func TestScaffoldedActionWorkflowsHaveIDTokenWrite(t *testing.T) {
	// Vars completas (todas las {{}} de los workflows con la action) para que el YAML
	// rendido quede limpio y parseable.
	vars := Vars{
		"project_name": "Acme",
		"language":     "es",
		"art_director": "on",
		"app_path":     "frontend",
	}

	for _, stack := range []string{"python-fastapi-react", "aiuda-flutter-firebase", "react-supabase"} {
		t.Run(stack, func(t *testing.T) {
			files, _, err := Render(realTemplates, stack, vars)
			if err != nil {
				t.Fatalf("Render(%s): %v", stack, err)
			}

			seenActionWorkflow := false
			for _, f := range files {
				if !strings.HasPrefix(f.Path, ".github/workflows/") || !strings.Contains(f.Content, "claude-code-action") {
					continue
				}
				// El render no debe dejar un {{var}} de template sin sustituir (rompería el
				// scaffold). Ojo: los workflows usan `${{ }}` de GitHub Actions — eso NO es un
				// var de template; solo chequeamos los nombres reales de var.
				for _, v := range []string{"{{project_name}}", "{{art_director}}", "{{app_path}}"} {
					if strings.Contains(f.Content, v) {
						t.Errorf("%s: quedó %s sin sustituir en el workflow rendido", f.Path, v)
					}
				}

				var wf ghWorkflow
				if err := yaml.Unmarshal([]byte(f.Content), &wf); err != nil {
					t.Fatalf("%s: YAML rendido no parsea: %v", f.Path, err)
				}

				for name, job := range wf.Jobs {
					if !jobUsesClaudeAction(job.Steps) {
						continue
					}
					seenActionWorkflow = true
					// Permiso EFECTIVO: el del job si lo declaró; si no, el de workflow.
					eff := wf.Permissions
					if job.Permissions != nil {
						eff = job.Permissions
					}
					if eff["id-token"] != "write" {
						t.Errorf("%s job %q corre claude-code-action pero su permissions efectivo no tiene `id-token: write` (tiene: %v) → \"Could not fetch an OIDC token\"", f.Path, name, eff)
					}
				}
			}

			if !seenActionWorkflow {
				t.Fatalf("%s: no se encontró ningún workflow con claude-code-action — el test no verificó nada (¿cambió el scaffold?)", stack)
			}
		})
	}
}
