package scaffold

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Regresión del fallo "firebase: not found → probe timeout 180s": e2e-verify boot-ea el
// emulator suite de Firebase (backend.boot del contrato) pero el runner no trae ni
// firebase-tools ni un JDK. El workflow debe INSTALARLOS antes de correr e2e_verify.py.
// El toolchain es DATA-driven (§1-bis, CI-enforced por verify_leakguard_test): el workflow
// genérico de _common instala runtimes genéricos (node/java/cache) leyendo QUÉ del contrato;
// los valores específicos de firebase viven en stack.verify.yaml. El test verifica los
// templates RENDIDOS (post-substitución), no el .tmpl crudo.

func renderFlutter(t *testing.T) map[string]string {
	t.Helper()
	vars := Vars{"project_name": "Acme", "language": "es", "art_director": "on", "app_path": "app"}
	files, _, err := Render(realTemplates, "aiuda-flutter-firebase", vars)
	if err != nil {
		t.Fatalf("Render(aiuda-flutter-firebase): %v", err)
	}
	return pathsOf(files)
}

func TestE2EVerifyGenericToolchainSetup(t *testing.T) {
	byPath := renderFlutter(t)
	wf, ok := byPath[".github/workflows/e2e-verify.yml"]
	if !ok {
		t.Fatalf("e2e-verify.yml no está en el scaffold flutter; got %v", keys(byPath))
	}
	if err := yaml.Unmarshal([]byte(wf), new(any)); err != nil {
		t.Fatalf("e2e-verify.yml rendido no parsea: %v", err)
	}

	// El workflow genérico instala los runtimes (leyendo QUÉ del contrato) con actions reales,
	// sin asumir el runner. NO debe nombrar frameworks (eso lo cubre verify_leakguard_test).
	for _, substr := range []string{
		"Read CI toolchain from contract", // lee e2e.ci_setup
		"actions/setup-node",              // Node runtime (con versión del contrato)
		"actions/setup-java",              // JDK (muchos emulators locales son JVM)
		"distribution: temurin",           // distribución del JDK
		"actions/cache",                   // cache de binarios del emulator (path del contrato)
	} {
		if !strings.Contains(wf, substr) {
			t.Errorf("e2e-verify.yml no tiene %q en el bloque de toolchain", substr)
		}
	}

	// Orden: node/java/cache se preparan ANTES de correr el orquestador (si no, el boot
	// no tiene el runtime). Anclamos en el step real, no en la mención del comentario.
	setup := strings.Index(wf, "actions/setup-node")
	run := strings.Index(wf, "e2e_verify.py --repo")
	if setup < 0 || run < 0 || setup > run {
		t.Errorf("el toolchain debe prepararse ANTES de e2e_verify.py (setup=%d run=%d)", setup, run)
	}
}

func TestFlutterContractSuppliesFirebaseToolchain(t *testing.T) {
	byPath := renderFlutter(t)
	contract, ok := byPath[".fluxo/verify/stack.verify.yaml"]
	if !ok {
		t.Fatalf("stack.verify.yaml no está en el scaffold flutter; got %v", keys(byPath))
	}
	// Debe parsear y declarar el ci_setup + el install del CLI. Acá SÍ es válido nombrar firebase.
	var doc struct {
		E2E struct {
			SetupCmd string `yaml:"setup_cmd"`
			CISetup  struct {
				Node      string `yaml:"node"`
				Java      string `yaml:"java"`
				CachePath string `yaml:"cache_path"`
			} `yaml:"ci_setup"`
		} `yaml:"e2e"`
	}
	if err := yaml.Unmarshal([]byte(contract), &doc); err != nil {
		t.Fatalf("stack.verify.yaml no parsea: %v", err)
	}
	if doc.E2E.CISetup.Node != "20" {
		t.Errorf("ci_setup.node = %q, want \"20\"", doc.E2E.CISetup.Node)
	}
	if doc.E2E.CISetup.Java != "21" {
		t.Errorf("ci_setup.java = %q, want \"21\"", doc.E2E.CISetup.Java)
	}
	if !strings.Contains(doc.E2E.CISetup.CachePath, "firebase/emulators") {
		t.Errorf("ci_setup.cache_path = %q, want el path del cache de emulators", doc.E2E.CISetup.CachePath)
	}
	if !strings.Contains(doc.E2E.SetupCmd, "npm install -g firebase-tools") {
		t.Errorf("setup_cmd no instala firebase-tools: %q", doc.E2E.SetupCmd)
	}
}

// Part-2 del audit: cada workflow del stack instala lo que usa. ui-verify arranca la app
// (Flutter web) y saca un screenshot con Chromium — debe traer ambos, sin asumir el runner.
func TestUIVerifyInstallsFlutterAndChromium(t *testing.T) {
	byPath := renderFlutter(t)
	wf, ok := byPath[".github/workflows/ui-verify.yml"]
	if !ok {
		t.Fatalf("ui-verify.yml no está en el scaffold flutter; got %v", keys(byPath))
	}
	for _, substr := range []string{
		"subosito/flutter-action", // Flutter SDK (flutter build web)
		"actions/setup-node",      // Node para Playwright
		"playwright install",      // instala el navegador…
		"chromium",                // …Chromium, el que saca el screenshot
	} {
		if !strings.Contains(wf, substr) {
			t.Errorf("ui-verify.yml no instala %q (arranca la app / saca el screenshot)", substr)
		}
	}
}
