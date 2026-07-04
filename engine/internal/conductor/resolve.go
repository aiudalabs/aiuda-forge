package conductor

// Conflict resolution (incidente PR #83): cuando el PR de un sprint choca con
// main (mergeStateStatus DIRTY / mergeable CONFLICTING), el auto-merge lo ignora
// —solo toma PRs CLEAN— y nadie lo resuelve. Esto despacha al canal claude_action
// un agente que hace exactamente lo que un humano hacía a mano: fetch de main,
// merge en la rama del PR, resolver preservando ambas intenciones, correr la
// suite y pushear a la MISMA rama. Un guard anti-loop evita re-despachar el mismo
// PR mientras una resolución sigue en vuelo.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"forge/internal/github"
)

// ErrResolveInFlight: ya hay una resolución despachada recientemente para este
// PR — el guard la bloquea hasta que expira el cooldown. La API la mapea a 409
// para que el usuario sepa que NO se re-despachó (y no gaste otra cuota).
var ErrResolveInFlight = errors.New("conflict resolution already in flight for this PR")

// resolveCooldown: ventana durante la cual un PR ya despachado no se re-despacha.
// Una resolución (fetch + merge + tests + push) tarda minutos; 30m cubre el ciclo
// con holgura sin dejar el PR bloqueado para siempre si la resolución no cerró.
const resolveCooldown = 30 * time.Minute

// ConflictDispatcher es la superficie mínima para despachar la resolución por el
// canal claude_action (implementada por *github.Client).
type ConflictDispatcher interface {
	DispatchWorkflow(ctx context.Context, repoURL, workflowFile, ref string, inputs map[string]string) error
}

// ConflictResolver despacha una resolución de conflicto contra main al canal
// claude_action, con un guard anti-loop por PR.
type ConflictResolver struct {
	GH ConflictDispatcher
	// ClientFor, si está presente, resuelve el cliente por PROYECTO (tenant).
	ClientFor func(ctx context.Context, projectID string) *github.Client

	now      func() time.Time // inyectable en tests; nil = time.Now
	mu       sync.Mutex
	inFlight map[string]time.Time // "slug#n" → momento del último despacho
}

// NewConflictResolver construye un resolver con el guard inicializado.
func NewConflictResolver(gh ConflictDispatcher) *ConflictResolver {
	return &ConflictResolver{GH: gh, inFlight: map[string]time.Time{}}
}

func (r *ConflictResolver) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// ghFor resuelve el dispatcher por proyecto (tenant) o cae al fijo del host.
func (r *ConflictResolver) ghFor(ctx context.Context, projectID string) ConflictDispatcher {
	if r.ClientFor != nil {
		if c := r.ClientFor(ctx, projectID); c != nil {
			return c
		}
	}
	return r.GH
}

// Resolve despacha UNA resolución del conflicto del PR number contra main. El
// guard anti-loop bloquea un segundo despacho dentro del cooldown
// (ErrResolveInFlight). Si el despacho falla, libera el guard para permitir un
// reintento inmediato.
func (r *ConflictResolver) Resolve(ctx context.Context, projectID, repoURL string, number int) error {
	key := repoSlug(repoURL) + "#" + strconv.Itoa(number)

	r.mu.Lock()
	if r.inFlight == nil {
		r.inFlight = map[string]time.Time{}
	}
	if last, ok := r.inFlight[key]; ok && r.clock().Sub(last) < resolveCooldown {
		r.mu.Unlock()
		return ErrResolveInFlight
	}
	r.inFlight[key] = r.clock()
	r.mu.Unlock()

	prompt := conflictPrompt(number)
	if err := r.ghFor(ctx, projectID).DispatchWorkflow(ctx, repoURL, claudeWorkflowFile, "main", map[string]string{"prompt": prompt}); err != nil {
		r.mu.Lock()
		delete(r.inFlight, key)
		r.mu.Unlock()
		return err
	}
	return nil
}

// conflictPrompt es el prompt de resolución: preserva AMBAS intenciones (el
// trabajo del PR y lo que llegó a main), corre la suite y pushea a la MISMA rama
// —nunca abre un PR nuevo—.
func conflictPrompt(number int) string {
	return fmt.Sprintf(`Resolve the merge conflicts of pull request #%d against the base branch (main), then push the resolution to the SAME branch. Do NOT open a new pull request.

Steps, in order:
1. Run: gh pr checkout %d   (this fetches and checks out the PR's head branch).
2. Bring in the base branch: git fetch origin main && git merge origin/main
3. Resolve EVERY conflict PRESERVING BOTH intentions — keep the work this PR introduced AND the changes that already landed on main. Reconcile them; never drop one side wholesale.
4. Run the repository's test suite and make it green.
5. Commit the merge resolution and push to the same head branch: git push

Pull request #%d must update in place — no new branch, no new PR.`, number, number, number)
}
