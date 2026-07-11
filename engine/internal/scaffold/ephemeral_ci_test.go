package scaffold

import (
	"strings"
	"testing"
)

// byPathContent indexa los archivos rendidos por su Path → Content.
func byPathContent(files []File) map[string]string {
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[f.Path] = f.Content
	}
	return m
}

// TestScaffoldEphemeralCIRule: la regla "CI efímero" (corré síncrono, nunca
// background+esperar, nunca programes un wakeup/check-in) debe quedar rendida en la
// constitución del repo (CLAUDE.md) y en la guía tool-neutral (AGENTS.md) de TODOS los
// stacks. Es la red persistente contra el incidente del Action colgado esperando un
// wakeup que nunca dispara.
func TestScaffoldEphemeralCIRule(t *testing.T) {
	for _, stack := range []string{"aiuda-flutter-firebase", "python-fastapi-react", "react-supabase"} {
		t.Run(stack, func(t *testing.T) {
			files, _, err := Render(realTemplates, stack, Vars{
				"project_name": "Acme", "language": "es", "art_director": "on", "app_path": "apps/customer",
			})
			if err != nil {
				t.Fatalf("Render(%s): %v", stack, err)
			}
			byPath := byPathContent(files)
			for _, doc := range []string{"CLAUDE.md", "AGENTS.md"} {
				content, ok := byPath[doc]
				if !ok {
					t.Fatalf("%s: falta %s en el scaffold", stack, doc)
				}
				for _, want := range []string{"CI runner is ephemeral", "never schedule a wakeup"} {
					if !strings.Contains(content, want) {
						t.Errorf("%s/%s no contiene la regla CI efímero %q", stack, doc, want)
					}
				}
			}
		})
	}
}

// TestScaffoldApkWorkflowFlutterOnly: el job de CI que construye el APK se scaffoldea
// SOLO en el stack Flutter (los stacks web no lo reciben), construye el APK de release
// y sube el artifact — el patrón determinista que reemplaza "el agente construye el APK".
func TestScaffoldApkWorkflowFlutterOnly(t *testing.T) {
	vars := Vars{"project_name": "Acme", "language": "es", "art_director": "on", "app_path": "apps/customer"}

	// Flutter: build-apk.yml presente, con el build real y el app_path sustituido.
	flutter, _, err := Render(realTemplates, "aiuda-flutter-firebase", vars)
	if err != nil {
		t.Fatalf("Render(flutter): %v", err)
	}
	apk, ok := byPathContent(flutter)[".github/workflows/build-apk.yml"]
	if !ok {
		t.Fatal("aiuda-flutter-firebase: falta .github/workflows/build-apk.yml")
	}
	for _, want := range []string{
		"flutter build apk --release",
		"actions/upload-artifact",
		"build/app/outputs/flutter-apk", // path del artifact (vía ${{ env.APP_PATH }})
		`APP_PATH: "apps/customer"`,      // {{app_path}} sustituido en el bloque env
	} {
		if !strings.Contains(apk, want) {
			t.Errorf("build-apk.yml no contiene %q", want)
		}
	}
	if strings.Contains(apk, "{{app_path}}") {
		t.Error("build-apk.yml dejó {{app_path}} sin sustituir")
	}

	// Stacks web: NO deben recibir el workflow de APK.
	for _, stack := range []string{"python-fastapi-react", "react-supabase"} {
		files, _, err := Render(realTemplates, stack, vars)
		if err != nil {
			t.Fatalf("Render(%s): %v", stack, err)
		}
		if _, ok := byPathContent(files)[".github/workflows/build-apk.yml"]; ok {
			t.Errorf("%s NO debería recibir build-apk.yml (es web-only)", stack)
		}
	}
}
