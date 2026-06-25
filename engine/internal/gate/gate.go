// Package gate runs the project's gate command in isolation with two integrity
// guarantees ported from v1:
//
//   - ANTI-TAMPER: the gate command file (.vibeforge-gate) is hashed and SEALED
//     before the agent runs, outside the agent's reach. If the agent edits the
//     gate to weaken it, the post-run hash differs and the gate fails.
//   - SUITE-INTEGRITY: the number of test markers is counted before and after;
//     if it drops (the agent deleted tests to go green), the gate fails.
//
// These are deterministic, security-critical checks — kernel code, never markdown.
package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"forge/internal/sandbox"
)

var (
	// ErrTampered means the gate command file changed after sealing.
	ErrTampered = errors.New("gate tampered: .vibeforge-gate changed after seal")
	// ErrSuiteShrank means fewer test markers exist than at seal time.
	ErrSuiteShrank = errors.New("suite integrity: test count decreased")
)

// GateFile is the conventional gate command file at the repo root.
const GateFile = ".vibeforge-gate"

// defaultTestPattern matches test markers across common languages (Go, Python,
// JS/TS). Configurable via Integrity.Pattern.
var defaultTestPattern = regexp.MustCompile(`func Test\w+|def test_\w+|\bit\(|\btest\(`)

// Integrity is a sealed snapshot of the gate's tamper-relevant state.
type Integrity struct {
	GateHash  string `json:"gate_hash"`
	TestCount int    `json:"test_count"`
	Pattern   string `json:"pattern"` // empty => default
}

// Snapshot computes the integrity state of the tree at workdir: the hash of the
// gate file and the count of test markers. A missing gate file hashes to "".
func Snapshot(workdir string, pattern *regexp.Regexp) (Integrity, error) {
	if pattern == nil {
		pattern = defaultTestPattern
	}
	hash := ""
	if b, err := os.ReadFile(filepath.Join(workdir, GateFile)); err == nil {
		sum := sha256.Sum256(b)
		hash = hex.EncodeToString(sum[:])
	} else if !os.IsNotExist(err) {
		return Integrity{}, err
	}
	count, err := countTestMarkers(workdir, pattern)
	if err != nil {
		return Integrity{}, err
	}
	return Integrity{GateHash: hash, TestCount: count}, nil
}

func countTestMarkers(workdir string, pattern *regexp.Regexp) (int, error) {
	total := 0
	err := filepath.WalkDir(workdir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil // unreadable file is not a test marker
		}
		total += len(pattern.FindAll(b, -1))
		return nil
	})
	return total, err
}

// Seal persists an integrity snapshot to a DAEMON-SIDE path (metaRoot), kept
// outside the agent's working tree so the agent cannot rewrite the reference.
func Seal(metaRoot, runID string, integ Integrity) error {
	if err := os.MkdirAll(metaRoot, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(integ, "", "  ")
	return os.WriteFile(sealPath(metaRoot, runID), b, 0o600)
}

// LoadSeal reads a previously sealed snapshot.
func LoadSeal(metaRoot, runID string) (Integrity, error) {
	b, err := os.ReadFile(sealPath(metaRoot, runID))
	if err != nil {
		return Integrity{}, err
	}
	var integ Integrity
	return integ, json.Unmarshal(b, &integ)
}

func sealPath(metaRoot, runID string) string {
	return filepath.Join(metaRoot, runID+".seal.json")
}

// CheckIntegrity compares the current tree against the sealed snapshot.
func CheckIntegrity(sealed, current Integrity) error {
	if sealed.GateHash != "" && current.GateHash != sealed.GateHash {
		return ErrTampered
	}
	if current.TestCount < sealed.TestCount {
		return fmt.Errorf("%w: was %d, now %d", ErrSuiteShrank, sealed.TestCount, current.TestCount)
	}
	return nil
}

// Result is the outcome of a hardened gate run.
type Result struct {
	Passed  bool
	Tamper  bool
	Output  string
	Sandbox string // executor used ("docker"/"local")
}

// Run executes the gate command in the sandbox and enforces integrity against
// the sealed snapshot. command is the gate command (inline or read from the gate
// file by the caller). It returns a Result; an integrity violation forces
// Passed=false even if the command itself would have exited 0.
func Run(ctx context.Context, sb sandbox.Sandbox, workdir, metaRoot, runID, command string) (Result, error) {
	// Anti-tamper: re-check the sealed state BEFORE trusting the gate.
	sealed, err := LoadSeal(metaRoot, runID)
	hasSeal := err == nil
	current, snapErr := Snapshot(workdir, nil)
	if snapErr != nil {
		return Result{}, snapErr
	}
	if hasSeal {
		if cerr := CheckIntegrity(sealed, current); cerr != nil {
			return Result{Passed: false, Tamper: true, Output: cerr.Error(), Sandbox: sb.Kind()}, nil
		}
	}

	out, code, runErr := sb.Exec(ctx, command)
	if runErr != nil {
		return Result{Passed: false, Output: "gate exec error: " + runErr.Error(), Sandbox: sb.Kind()}, nil
	}
	return Result{Passed: code == 0, Output: out, Sandbox: sb.Kind()}, nil
}
