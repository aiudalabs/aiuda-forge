package gate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"forge/internal/sandbox"
	"forge/internal/workflow"
)

// setupRun creates the conventional workdir layout (root/<runID>) with a gate
// file and a test file, then seals it. Returns (workdir, runID).
func setupRun(t *testing.T, gateCmd, testContent string) (string, string) {
	t.Helper()
	root := t.TempDir()
	runID := "run_abc"
	workdir := filepath.Join(root, runID)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, GateFile), []byte(gateCmd), 0o755); err != nil {
		t.Fatal(err)
	}
	if testContent != "" {
		if err := os.WriteFile(filepath.Join(workdir, "test_thing.py"), []byte(testContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SealWorkdir(workdir); err != nil {
		t.Fatalf("seal: %v", err)
	}
	return workdir, runID
}

// TestAntiTamperDetectsGateEdit: if the agent rewrites .vibeforge-gate after the
// seal, the hardened gate fails with tamper=true even if the (weakened) command
// would exit 0.
func TestAntiTamperDetectsGateEdit(t *testing.T) {
	workdir, _ := setupRun(t, "exit 1\n", "def test_a(): pass\n")

	// Agent weakens the gate to always pass.
	if err := os.WriteFile(filepath.Join(workdir, GateFile), []byte("exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewHardenedRunner()
	runner.SandboxTemplate = sandbox.Config{Runtime: "local"} // deterministic, no docker dependency
	step := workflow.Step{ID: "gate", Type: "gate", CommandFrom: "repo"}
	res, err := runner.Run(context.Background(), step, nil, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatalf("tampered gate must NOT pass")
	}
	if tamper, _ := res.Output["tamper"].(bool); !tamper {
		t.Fatalf("expected tamper flag set, got output %v / detail %q", res.Output, res.Detail)
	}
}

// TestSuiteIntegrityDetectsDeletedTests: if the agent deletes a test file after
// the seal, the test marker count drops and integrity fails.
func TestSuiteIntegrityDetectsDeletedTests(t *testing.T) {
	workdir, runID := setupRun(t, "exit 0\n", "def test_a(): pass\ndef test_b(): pass\n")

	// Agent removes the tests to "go green".
	if err := os.Remove(filepath.Join(workdir, "test_thing.py")); err != nil {
		t.Fatal(err)
	}

	sb := sandbox.New(sandbox.Config{Runtime: "local", Workdir: workdir})
	res, err := Run(context.Background(), sb, workdir, MetaRootFor(workdir), runID, "exit 0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatalf("deleting tests must fail the gate (suite integrity)")
	}
	if !res.Tamper {
		t.Fatalf("expected integrity violation flagged")
	}
}

// TestCleanGatePasses: an honest run (no tamper, tests intact, command exits 0)
// passes.
func TestCleanGatePasses(t *testing.T) {
	workdir, _ := setupRun(t, "exit 0\n", "def test_a(): pass\n")
	runner := NewHardenedRunner()
	runner.SandboxTemplate = sandbox.Config{Runtime: "local"}
	step := workflow.Step{ID: "gate", Type: "gate", CommandFrom: "repo"}
	res, err := runner.Run(context.Background(), step, nil, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("clean gate should pass, detail=%q", res.Detail)
	}
}

// TestCheckIntegrity unit-tests the comparison directly.
func TestCheckIntegrity(t *testing.T) {
	sealed := Integrity{GateHash: "abc", TestCount: 5}
	if err := CheckIntegrity(sealed, Integrity{GateHash: "abc", TestCount: 5}); err != nil {
		t.Fatalf("identical should pass: %v", err)
	}
	if err := CheckIntegrity(sealed, Integrity{GateHash: "abc", TestCount: 7}); err != nil {
		t.Fatalf("more tests is fine: %v", err)
	}
	if err := CheckIntegrity(sealed, Integrity{GateHash: "different", TestCount: 5}); err == nil {
		t.Fatalf("changed gate hash must be tamper")
	}
	if err := CheckIntegrity(sealed, Integrity{GateHash: "abc", TestCount: 4}); err == nil {
		t.Fatalf("fewer tests must fail")
	}
}
