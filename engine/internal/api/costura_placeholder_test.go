package api

import (
	"testing"

	"forge/internal/scaffold"
)

// TestDropUnresolvedFiles: el fail-loud de la costura descarta SOLO los archivos con un
// placeholder `{{var}}` sin resolver, y NO se confunde con las expresiones `${{ ... }}` de
// GitHub Actions (que también contienen `{{`). Bloquea la regresión que dejó rutaviva con
// `APP_PATH: "{{app_path}}"` commiteado y el build saltándose en verde-vacío.
func TestDropUnresolvedFiles(t *testing.T) {
	files := []scaffold.File{
		// tiene el placeholder {{app_path}} sin resolver → debe descartarse
		{Path: ".github/workflows/ui-verify.yml", Content: "env:\n  APP_PATH: \"{{app_path}}\"\n"},
		// contenido limpio → se queda
		{Path: "AGENTS.md", Content: "project Acme, todo sustituido"},
		// SOLO expresiones ${{ }} de Actions (no un {{var}} de scaffold) → se queda
		{Path: ".github/workflows/claude.yml", Content: "token: ${{ secrets.T }}\nif: ${{ github.event_name == 'push' }}\n"},
	}
	clean, dropped := dropUnresolvedFiles(files, []string{"app_path"})

	if len(dropped) != 1 || dropped[0] != ".github/workflows/ui-verify.yml" {
		t.Fatalf("dropped = %v, want [.github/workflows/ui-verify.yml]", dropped)
	}
	if len(clean) != 2 {
		t.Fatalf("clean = %d, want 2 (AGENTS.md + claude.yml con solo ${{ }})", len(clean))
	}
	for _, f := range clean {
		if f.Path == ".github/workflows/ui-verify.yml" {
			t.Fatal("el archivo con placeholder {{app_path}} no debió quedar en clean")
		}
	}
}
