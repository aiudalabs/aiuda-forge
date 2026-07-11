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

// TestScaffoldBuildabilitySelfHeal: la buildabilidad es contract-driven. El contrato del
// stack Flutter declara `build.bootstrap` (el `flutter create` que materializa las carpetas
// de plataforma), y los workflows de build (build-apk web/apk + ui-verify) tienen un paso
// self-heal que lee ESE comando del contrato y lo corre si la plataforma falta — cerrando el
// "bug #1" (apps con lib/ pero sin android/) que hoy el provisioning-lint solo detecta.
func TestScaffoldBuildabilitySelfHeal(t *testing.T) {
	files, _, err := Render(realTemplates, "aiuda-flutter-firebase", Vars{
		"project_name": "Acme", "language": "es", "art_director": "on", "app_path": "apps/customer",
	})
	if err != nil {
		t.Fatalf("Render(flutter): %v", err)
	}
	byPath := byPathContent(files)

	// 1) El contrato declara build.bootstrap (DATA compartida self-heal + foundation story).
	contract, ok := byPath[".fluxo/verify/stack.verify.yaml"]
	if !ok {
		t.Fatal("falta .fluxo/verify/stack.verify.yaml")
	}
	for _, want := range []string{"build:", "bootstrap:", "flutter create --platforms=android,ios,web ."} {
		if !strings.Contains(contract, want) {
			t.Errorf("el contrato no declara %q", want)
		}
	}

	// 2) build-apk (android) y ui-verify (web) tienen el paso self-heal que lee el contrato.
	checks := map[string][]string{
		".github/workflows/build-apk.yml": {"Ensure Android platform (self-heal from contract)", "stack.verify.yaml", "flutter create"},
		".github/workflows/ui-verify.yml": {"Ensure web platform (self-heal from contract)", "stack.verify.yaml", "flutter create"},
	}
	for path, wants := range checks {
		content, ok := byPath[path]
		if !ok {
			t.Fatalf("falta %s", path)
		}
		for _, w := range wants {
			if !strings.Contains(content, w) {
				t.Errorf("%s no contiene %q (self-heal contract-driven)", path, w)
			}
		}
	}
}
