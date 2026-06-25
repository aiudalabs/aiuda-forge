package tickets

import "context"

// TicketProvider is the interface the orchestrator (B2) will consume. It
// presents the native store as a provider-agnostic backlog surface. GitHub /
// JIRA become optional sync adapters that implement this same interface.
type TicketProvider interface {
	ListStories(ctx context.Context) ([]Story, error)
	GetStory(ctx context.Context, id string) (Story, error)
	CreateStory(ctx context.Context, s Story) error
	UpdateStatus(ctx context.Context, id string, status Status) error
	Ready(ctx context.Context) ([]Story, error)
	// ClaimStory atomically transitions a story from backlog → running.
	// Returns true if this caller claimed it, false if already taken.
	ClaimStory(ctx context.Context, id string) (bool, error)
	// MarkFailed sets a story's status to failed. Called when the run driving
	// the story reaches a terminal non-DONE state (FAILED, CANCELLED).
	MarkFailed(ctx context.Context, id string) error

	// ---- Sprint-batched (goal mode) -----------------------------------------

	// ReadySprints returns sprints that can be fired as a single goal-mode run
	// (≥1 story, all backlog, external deps done).
	ReadySprints(ctx context.Context) ([]Sprint, error)
	// StoriesBySprint returns a sprint's stories in intra-sprint topological order.
	StoriesBySprint(ctx context.Context, sprintID string) ([]Story, error)
	// ClaimSprint atomically claims all of a sprint's backlog stories at once.
	// ok=false (no error) if a concurrent claimer already moved any of them.
	ClaimSprint(ctx context.Context, sprintID string) (claimed []string, ok bool, err error)
	// MarkSprintDone / MarkSprintFailed advance all the sprint's running stories.
	MarkSprintDone(ctx context.Context, sprintID string) error
	MarkSprintFailed(ctx context.Context, sprintID string) error
	// SetSprintRun records the firing run_id on every story in the sprint.
	SetSprintRun(ctx context.Context, sprintID, runID string) error
}

// NativeProvider implements TicketProvider backed by Store.
type NativeProvider struct {
	store *Store
}

// NewNativeProvider wraps a Store as a TicketProvider.
func NewNativeProvider(st *Store) *NativeProvider { return &NativeProvider{store: st} }

func (p *NativeProvider) ListStories(_ context.Context) ([]Story, error) {
	return p.store.ListStories()
}

func (p *NativeProvider) GetStory(_ context.Context, id string) (Story, error) {
	return p.store.GetStory(id)
}

func (p *NativeProvider) CreateStory(_ context.Context, s Story) error {
	return p.store.CreateStory(s)
}

func (p *NativeProvider) UpdateStatus(_ context.Context, id string, status Status) error {
	return p.store.UpdateStoryStatus(id, status)
}

func (p *NativeProvider) Ready(_ context.Context) ([]Story, error) {
	return p.store.Ready()
}

func (p *NativeProvider) ClaimStory(_ context.Context, id string) (bool, error) {
	return p.store.ClaimStory(id)
}

func (p *NativeProvider) MarkFailed(_ context.Context, id string) error {
	return p.store.MarkFailed(id)
}

func (p *NativeProvider) ReadySprints(_ context.Context) ([]Sprint, error) {
	return p.store.ReadySprints()
}

func (p *NativeProvider) StoriesBySprint(_ context.Context, sprintID string) ([]Story, error) {
	return p.store.StoriesBySprint(sprintID)
}

func (p *NativeProvider) ClaimSprint(_ context.Context, sprintID string) ([]string, bool, error) {
	return p.store.ClaimSprint(sprintID)
}

func (p *NativeProvider) MarkSprintDone(_ context.Context, sprintID string) error {
	return p.store.MarkSprintDone(sprintID)
}

func (p *NativeProvider) MarkSprintFailed(_ context.Context, sprintID string) error {
	return p.store.MarkSprintFailed(sprintID)
}

func (p *NativeProvider) SetSprintRun(_ context.Context, sprintID, runID string) error {
	return p.store.SetSprintRun(sprintID, runID)
}
