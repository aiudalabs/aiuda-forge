package workflow

import "context"

// StepEmitter persists a single fine-grained event produced WHILE a step runs
// (a tool call, an assistant text chunk, a system notice). It is bound to one
// (runID, taskID) and writes a step.event row. Unlike StepResult.Events — which
// are forwarded once, after the step finishes — these stream live, dozens per
// long run, forming the auditable terminal-log of what the agent actually did.
type StepEmitter func(eventType string, data map[string]any)

// emitterKey is the unexported context key for the per-step emitter. Threading
// it through context.Context (rather than a field on the shared Runner) keeps
// concurrent runs isolated: each runner.Run sees only its own run's emitter.
type emitterKey struct{}

// WithEmitter returns a child context carrying fn as the active step emitter.
// The engine sets this just before runner.Run; the runner pulls it via
// EmitterFrom and feeds the backend's streamed events into it.
func WithEmitter(ctx context.Context, fn StepEmitter) context.Context {
	return context.WithValue(ctx, emitterKey{}, fn)
}

// EmitterFrom returns the step emitter on ctx, or nil if none was set (e.g. a
// unit test driving the runner directly). Callers must nil-check.
func EmitterFrom(ctx context.Context) StepEmitter {
	fn, _ := ctx.Value(emitterKey{}).(StepEmitter)
	return fn
}

// OwnershipCheck reports whether THIS worker still owns the task's live claim —
// its fence is current and the task is still RUNNING under it. It is threaded
// through ctx (like the emitter) so a runner with an irreversible filesystem
// side effect can skip it when the worker was reaped and the task re-claimed by
// another worker. Returns true when no check was installed (unit tests, or a
// step with no such side effect): the absence of a guard must not block work.
type OwnershipCheck func() bool

type ownershipKey struct{}

// WithOwnershipCheck returns a child context carrying fn as the active ownership
// check. The engine sets this just before runner.Run, bound to (task.ID, fence).
func WithOwnershipCheck(ctx context.Context, fn OwnershipCheck) context.Context {
	return context.WithValue(ctx, ownershipKey{}, fn)
}

// StillOwnsWorkdir reports whether the worker still owns the workdir per the
// ownership check on ctx. It returns true when no check is installed (so unit
// tests and side-effect-free steps are unaffected — the guard is opt-in).
func StillOwnsWorkdir(ctx context.Context) bool {
	fn, _ := ctx.Value(ownershipKey{}).(OwnershipCheck)
	if fn == nil {
		return true
	}
	return fn()
}
