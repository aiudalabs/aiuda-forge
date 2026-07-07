package scaffold

import (
	"context"
	"reflect"
	"testing"
)

// fakeProtector records the contexts passed to AddRequiredStatusChecks.
type fakeProtector struct {
	calls    int
	repoURL  string
	branch   string
	contexts []string
}

func (f *fakeProtector) AddRequiredStatusChecks(_ context.Context, repoURL, branch string, contexts []string) error {
	f.calls++
	f.repoURL = repoURL
	f.branch = branch
	f.contexts = contexts
	return nil
}

func TestEnsureRequiredChecksNormalizesAndThreads(t *testing.T) {
	fp := &fakeProtector{}
	// Duplicates, blanks, whitespace, and out-of-order input.
	in := []string{"e2e-verify", " provisioning-lint ", "e2e-verify", "", "  ", "provisioning-lint"}

	if err := EnsureRequiredChecks(context.Background(), fp, "https://github.com/o/r", "main", in); err != nil {
		t.Fatalf("EnsureRequiredChecks: %v", err)
	}
	if fp.calls != 1 {
		t.Fatalf("expected 1 call, got %d", fp.calls)
	}
	if fp.repoURL != "https://github.com/o/r" || fp.branch != "main" {
		t.Errorf("repoURL/branch not threaded: %q %q", fp.repoURL, fp.branch)
	}
	// Deduped, trimmed, sorted.
	want := []string{"e2e-verify", "provisioning-lint"}
	if !reflect.DeepEqual(fp.contexts, want) {
		t.Errorf("contexts = %v, want %v", fp.contexts, want)
	}
}

func TestEnsureRequiredChecksNoOpOnEmpty(t *testing.T) {
	for _, in := range [][]string{nil, {}, {"", "   "}} {
		fp := &fakeProtector{}
		if err := EnsureRequiredChecks(context.Background(), fp, "r", "main", in); err != nil {
			t.Fatalf("EnsureRequiredChecks(%v): %v", in, err)
		}
		if fp.calls != 0 {
			t.Errorf("input %v should make no call (nothing to require), got %d", in, fp.calls)
		}
	}
}
