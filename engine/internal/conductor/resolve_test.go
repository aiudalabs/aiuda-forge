package conductor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeResolveGH captura cada DispatchWorkflow y puede inyectar un error.
type fakeResolveGH struct {
	calls []resolveCall
	err   error
}

type resolveCall struct {
	repoURL      string
	workflowFile string
	ref          string
	prompt       string
}

func (f *fakeResolveGH) DispatchWorkflow(_ context.Context, repoURL, workflowFile, ref string, inputs map[string]string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, resolveCall{repoURL, workflowFile, ref, inputs["prompt"]})
	return nil
}

func TestResolveDispatchesConflictPrompt(t *testing.T) {
	gh := &fakeResolveGH{}
	r := NewConflictResolver(gh)

	if err := r.Resolve(context.Background(), "p1", "https://github.com/o/r", 42); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(gh.calls) != 1 {
		t.Fatalf("dispatches = %d, want 1", len(gh.calls))
	}
	c := gh.calls[0]
	if c.workflowFile != claudeWorkflowFile || c.ref != "main" {
		t.Fatalf("workflow/ref = %s/%s, want %s/main", c.workflowFile, c.ref, claudeWorkflowFile)
	}
	for _, want := range []string{"#42", "gh pr checkout 42", "PRESERVING BOTH", "no new PR", "git push"} {
		if !strings.Contains(c.prompt, want) {
			t.Fatalf("prompt sin %q:\n%s", want, c.prompt)
		}
	}
}

func TestResolveGuardBlocksInFlight(t *testing.T) {
	gh := &fakeResolveGH{}
	now := time.Now()
	r := NewConflictResolver(gh)
	r.now = func() time.Time { return now }
	repo := "https://github.com/o/r"

	if err := r.Resolve(context.Background(), "p1", repo, 42); err != nil {
		t.Fatalf("primer Resolve: %v", err)
	}
	// Segundo despacho del MISMO PR dentro del cooldown → bloqueado, sin re-fire.
	if err := r.Resolve(context.Background(), "p1", repo, 42); !errors.Is(err, ErrResolveInFlight) {
		t.Fatalf("re-Resolve dentro del cooldown = %v, want ErrResolveInFlight", err)
	}
	// OTRO PR del mismo repo no está gateado por el guard del #42.
	if err := r.Resolve(context.Background(), "p1", repo, 43); err != nil {
		t.Fatalf("Resolve de otro PR: %v", err)
	}
	if len(gh.calls) != 2 {
		t.Fatalf("dispatches = %d, want 2 (#42 + #43; el re-#42 bloqueado)", len(gh.calls))
	}
	// Pasado el cooldown, el #42 vuelve a poder despacharse.
	now = now.Add(resolveCooldown + time.Minute)
	if err := r.Resolve(context.Background(), "p1", repo, 42); err != nil {
		t.Fatalf("Resolve tras cooldown: %v", err)
	}
	if len(gh.calls) != 3 {
		t.Fatalf("dispatches = %d, want 3 tras cooldown", len(gh.calls))
	}
}

func TestResolveReleasesGuardOnDispatchError(t *testing.T) {
	gh := &fakeResolveGH{err: errors.New("workflow_dispatch 404")}
	r := NewConflictResolver(gh)
	repo := "https://github.com/o/r"

	if err := r.Resolve(context.Background(), "p1", repo, 42); err == nil {
		t.Fatal("Resolve debería propagar el error del dispatch")
	}
	// El guard se liberó → un reintento inmediato NO devuelve ErrResolveInFlight.
	gh.err = nil
	if err := r.Resolve(context.Background(), "p1", repo, 42); err != nil {
		t.Fatalf("reintento tras fallo = %v, want nil (guard liberado)", err)
	}
	if len(gh.calls) != 1 {
		t.Fatalf("dispatches = %d, want 1 (el primero falló sin registrar call)", len(gh.calls))
	}
}
