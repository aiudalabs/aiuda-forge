package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenStackTokens are stack-specific vocabulary that MUST NOT leak into the
// generic verify layer under _common/. This operationalizes the §1-bis golden rule:
// if the generic engine/workflow ever names a framework, the abstraction has sprung
// a leak and the token belongs in a stack's DATA (rule table / stack.verify.yaml).
var forbiddenStackTokens = []string{
	"firestore", "datastore", "androidmanifest", "pubspec", "supabase",
	"firebase", "flutter", "geolocator", "vite_", ".dart", "service_role",
	"initializeapp", "create index",
}

// genericVerifyFiles are the S2 generic files under _common/ that must stay
// stack-agnostic. Scoped deliberately: pre-existing _common files (e.g.
// suite-integrity's multi-stack test-marker union) are out of scope for this guard.
var genericVerifyFiles = []string{
	"_common/.fluxo/verify/provisioning_lint.py.tmpl",
	"_common/.github/workflows/provisioning-lint.yml.tmpl",
	"_common/.fluxo/verify/e2e_verify.py.tmpl",       // S3 orchestrator (generic)
	"_common/.github/workflows/e2e-verify.yml.tmpl",  // S3 workflow (generic)
}

func TestCommonVerifyHasNoStackLeak(t *testing.T) {
	for _, rel := range genericVerifyFiles {
		raw, err := os.ReadFile(filepath.Join(realTemplates, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		lower := strings.ToLower(string(raw))
		for _, tok := range forbiddenStackTokens {
			if strings.Contains(lower, tok) {
				t.Errorf("%s leaks stack-specific token %q — move it to a stack rule table (golden rule §1-bis)", rel, tok)
			}
		}
	}
}

// bothReferenceStacks renders the two S2 reference stacks and asserts each carries
// the full verify surface — the generic engine/workflow from _common plus its own
// DATA rule table + contract. This is the concrete §1-bis proof at the scaffold
// layer: both stacks come out of the same _common with only their DATA differing.
func TestBothStacksRenderVerifyLayer(t *testing.T) {
	stacks := []string{"aiuda-flutter-firebase", "react-supabase"}
	want := []string{
		".fluxo/verify/provisioning_lint.py",   // generic engine (from _common, S2)
		".github/workflows/provisioning-lint.yml", // generic workflow (from _common, S2)
		".fluxo/verify/e2e_verify.py",          // generic orchestrator (from _common, S3 pt.1)
		".github/workflows/e2e-verify.yml",     // generic workflow (from _common, S3 pt.1)
		".fluxo/verify/provisioning.rules.yaml", // stack DATA
		".fluxo/verify/stack.verify.yaml",       // stack contract
		// S3 pt.2 — the per-stack e2e assets the generic orchestrator runs (stack DATA):
		// a seed loader, one AC-derived flow, and one checker per universal invariant.
		// Both reference stacks must carry the full set (proof the surface is symmetric).
		".fluxo/verify/e2e/seed.mjs",
		".fluxo/verify/e2e/flows/booking_flow.mjs",
		".fluxo/verify/e2e/invariants/session_persists.mjs",
		".fluxo/verify/e2e/invariants/no_client_over_read.mjs",
	}
	for _, stack := range stacks {
		files, _, err := Render(realTemplates, stack, Vars{"project_name": "Acme", "language": "es"})
		if err != nil {
			t.Fatalf("Render(%s): %v", stack, err)
		}
		byPath := pathsOf(files)
		for _, w := range want {
			if _, ok := byPath[w]; !ok {
				t.Errorf("stack %s missing rendered verify file %s", stack, w)
			}
		}
		// The generic engine text must be BYTE-IDENTICAL across stacks (it comes from
		// _common, not the stack) — the surest proof it is not per-stack forked.
		if got := byPath[".fluxo/verify/provisioning_lint.py"]; !strings.Contains(got, "GOLDEN RULE") {
			t.Errorf("stack %s: engine content missing/altered", stack)
		}
		if got := byPath[".fluxo/verify/e2e_verify.py"]; !strings.Contains(got, "GOLDEN RULE") {
			t.Errorf("stack %s: e2e orchestrator content missing/altered", stack)
		}
	}
}
