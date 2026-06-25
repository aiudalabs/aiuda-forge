package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDirLoaderTraversal: DirLoader must reject ids that contain path separators
// or other characters outside ^[A-Za-z0-9_-]+$, preventing an agent step from
// using agent: "../../etc/passwd" to escape the registry root.
func TestDirLoaderTraversal(t *testing.T) {
	// Build a minimal registry dir with one valid agent.
	root := t.TempDir()
	valid := "id: dev\nversion: 1.0.0\nmodel: claude-sonnet-4-6\nrole: dev\n"
	if err := os.WriteFile(filepath.Join(root, "dev.yaml"), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewDirLoader(root)

	// Valid id — must succeed.
	if _, err := loader.Load("dev"); err != nil {
		t.Fatalf("valid id 'dev': unexpected error: %v", err)
	}

	// Traversal ids — must be rejected before any file I/O.
	traversals := []string{
		"../../etc/passwd",
		"../dev",
		"..",
		"a/b",
		"a\x00b", // null byte
		"",
	}
	for _, id := range traversals {
		if _, err := loader.Load(id); err == nil {
			t.Errorf("traversal id %q: expected error, got nil", id)
		}
	}
}
