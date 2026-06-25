package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NativeTicket is the orchestrator's view of a control-plane story.
type NativeTicket struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	RunID    string   `json:"run_id"`
	Deps     []string `json:"deps"`
	SprintID string   `json:"sprint_id"` // empty in story mode; set when the story belongs to a sprint
	PRURL    string   `json:"pr_url"`    // recorded when in_review; the PR the reconcile loop checks for merge
	Repo     string   `json:"repo"`      // the repo the PR lives in (needed to address it via gh)
}

// StoryProvider is the interface through which NativeScheduler reads stories
// and writes status back. Tests use a fake; production uses NativeHTTPProvider.
type StoryProvider interface {
	// Ready returns stories whose deps are all done (derived "ready" — status
	// "backlog" with every dep at "done" in the control-plane store).
	Ready(ctx context.Context) ([]NativeTicket, error)
	// Running returns stories currently executing.
	Running(ctx context.Context) ([]NativeTicket, error)
	// Claim atomically transitions a story from backlog → running.
	// Returns true if this caller claimed it, false if already taken.
	Claim(ctx context.Context, id string) (bool, error)
	// MarkRunning records the run_id on a story that was claimed.
	MarkRunning(ctx context.Context, id, runID string) error
	// MarkInReview moves a story whose run finished (PR opened, not yet merged) to
	// in_review and records the PR URL so the reconcile loop can check the merge.
	MarkInReview(ctx context.Context, id, prURL string) error
	// MarkDone advances a story to done — called when its in_review PR is MERGED.
	MarkDone(ctx context.Context, id string) error
	// MarkFailed advances a story to failed when its run ends in a terminal
	// non-DONE state (FAILED, CANCELLED) so it does not stay stuck.
	MarkFailed(ctx context.Context, id string) error
	// InReview lists stories awaiting a merge (status in_review, PR recorded) so
	// the merge-reconcile loop can poll whether each PR has been merged.
	InReview(ctx context.Context) ([]NativeTicket, error)
	// GetStory fetches the full story (body, acceptance, owner) so the fired run
	// carries the complete context — not just the title.
	GetStory(ctx context.Context, id string) (NativeStory, error)

	// ---- Sprint-batched (goal mode) -----------------------------------------

	// ReadySprints returns sprints that can be fired as a single goal-mode run
	// (≥1 story, all backlog, external deps done).
	ReadySprints(ctx context.Context) ([]NativeSprint, error)
	// SprintStories returns a sprint's full stories in intra-sprint topological
	// order — the order the combined goal-mode ticket renders them.
	SprintStories(ctx context.Context, sprintID string) ([]NativeStory, error)
	// ClaimSprint atomically claims ALL of a sprint's backlog stories at once.
	// Returns the claimed story IDs (topo order) and ok=true on success; ok=false
	// (no error) if a concurrent claimer already moved any of them.
	ClaimSprint(ctx context.Context, sprintID string) (claimed []string, ok bool, err error)
	// MarkSprintRunning records the firing run_id on all the sprint's stories.
	MarkSprintRunning(ctx context.Context, sprintID, runID string) error
	// MarkSprintInReview moves all the sprint's running stories to in_review and
	// records the shared PR URL the goal-mode run opened.
	MarkSprintInReview(ctx context.Context, sprintID, prURL string) error
	// MarkSprintDone / MarkSprintFailed advance all the sprint's running stories.
	// MarkSprintDone is called when the sprint's in_review PR is MERGED.
	MarkSprintDone(ctx context.Context, sprintID string) error
	MarkSprintFailed(ctx context.Context, sprintID string) error
}

// NativeSprint is the scheduler's view of a sprint for goal-mode batching.
type NativeSprint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Goal string `json:"goal"`
}

// NativeStory is the full story the scheduler passes to a run as context.
// Repo is the GitHub repository URL the factory agent clones to implement
// the story; it flows from the design run payload → story.repo → run payload.
type NativeStory struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Accept string `json:"acceptance"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
}

// NativeHTTPProvider implements StoryProvider against the control-plane HTTP API.
type NativeHTTPProvider struct {
	baseURL string
	http    *http.Client
}

// NewNativeHTTPProvider returns a StoryProvider that talks to the control-plane
// at baseURL (e.g. "http://localhost:8080").
func NewNativeHTTPProvider(baseURL string) *NativeHTTPProvider {
	return &NativeHTTPProvider{baseURL: baseURL, http: &http.Client{}}
}

type ticketsListResp struct {
	Tickets []NativeTicket `json:"tickets"`
}

// Ready GETs /tickets and returns stories whose status is "ready" (the
// control-plane's compat endpoint already derives readiness from the dep graph).
func (p *NativeHTTPProvider) Ready(ctx context.Context) ([]NativeTicket, error) {
	return p.filterTickets(ctx, "ready")
}

// Running GETs /tickets and returns stories whose status is "running".
func (p *NativeHTTPProvider) Running(ctx context.Context) ([]NativeTicket, error) {
	return p.filterTickets(ctx, "running")
}

func (p *NativeHTTPProvider) filterTickets(ctx context.Context, status string) ([]NativeTicket, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/tickets", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get /tickets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get /tickets: status %d", resp.StatusCode)
	}

	var body ticketsListResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode /tickets: %w", err)
	}

	var out []NativeTicket
	for _, t := range body.Tickets {
		if t.Status == status {
			out = append(out, t)
		}
	}
	return out, nil
}

type storyStatusReq struct {
	Status string `json:"status"`
	RunID  string `json:"run_id,omitempty"`
	PRURL  string `json:"pr_url,omitempty"`
}

type claimResp struct {
	Claimed bool `json:"claimed"`
}

// Claim POSTs /stories/{id}/claim. Returns true if this caller claimed it.
func (p *NativeHTTPProvider) Claim(ctx context.Context, id string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/stories/"+id+"/claim", nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("post /stories/%s/claim: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return false, nil // already claimed by someone else
	}
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("post /stories/%s/claim: status %d", id, resp.StatusCode)
	}
	var body claimResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode claim response: %w", err)
	}
	return body.Claimed, nil
}

// MarkRunning PUTs /stories/{id}/status with status=running and the run_id.
// The story must already be claimed (status=running from Claim) before this is
// called; this call only records the run_id on an already-running story.
func (p *NativeHTTPProvider) MarkRunning(ctx context.Context, id, runID string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "running", RunID: runID})
}

// MarkInReview PUTs /stories/{id}/status with status=in_review and the PR URL.
func (p *NativeHTTPProvider) MarkInReview(ctx context.Context, id, prURL string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "in_review", PRURL: prURL})
}

// MarkDone PUTs /stories/{id}/status with status=done.
func (p *NativeHTTPProvider) MarkDone(ctx context.Context, id string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "done"})
}

// InReview GETs /tickets and returns stories whose status is "in_review".
func (p *NativeHTTPProvider) InReview(ctx context.Context) ([]NativeTicket, error) {
	return p.filterTickets(ctx, "in_review")
}

// MarkFailed PUTs /stories/{id}/status with status=failed.
func (p *NativeHTTPProvider) MarkFailed(ctx context.Context, id string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "failed"})
}

// GetStory GETs /stories/{id} and returns the full story.
func (p *NativeHTTPProvider) GetStory(ctx context.Context, id string) (NativeStory, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/stories/"+id, nil)
	if err != nil {
		return NativeStory{}, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return NativeStory{}, fmt.Errorf("get /stories/%s: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return NativeStory{}, fmt.Errorf("get /stories/%s: status %d", id, resp.StatusCode)
	}
	var st NativeStory
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return NativeStory{}, fmt.Errorf("decode story: %w", err)
	}
	return st, nil
}

// ---- Sprint-batched HTTP methods --------------------------------------------

type sprintsListResp struct {
	Sprints []NativeSprint `json:"sprints"`
}

type sprintStoriesResp struct {
	Stories []NativeStory `json:"stories"`
}

type sprintClaimResp struct {
	Claimed []string `json:"claimed"`
}

// ReadySprints GETs /sprints/ready.
func (p *NativeHTTPProvider) ReadySprints(ctx context.Context) ([]NativeSprint, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/sprints/ready", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get /sprints/ready: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get /sprints/ready: status %d", resp.StatusCode)
	}
	var body sprintsListResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode /sprints/ready: %w", err)
	}
	return body.Sprints, nil
}

// SprintStories GETs /sprints/{id}/stories (already topo-ordered server-side).
func (p *NativeHTTPProvider) SprintStories(ctx context.Context, sprintID string) ([]NativeStory, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/sprints/"+sprintID+"/stories", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get /sprints/%s/stories: %w", sprintID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get /sprints/%s/stories: status %d", sprintID, resp.StatusCode)
	}
	var body sprintStoriesResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode sprint stories: %w", err)
	}
	return body.Stories, nil
}

// ClaimSprint POSTs /sprints/{id}/claim. Returns the claimed IDs and ok=true on
// success; ok=false on 409 (a concurrent claimer already moved a story).
func (p *NativeHTTPProvider) ClaimSprint(ctx context.Context, sprintID string) ([]string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/sprints/"+sprintID+"/claim", nil)
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("post /sprints/%s/claim: %w", sprintID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil, false, nil // already claimed by someone else
	}
	if resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("post /sprints/%s/claim: status %d", sprintID, resp.StatusCode)
	}
	var body sprintClaimResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, false, fmt.Errorf("decode sprint claim: %w", err)
	}
	return body.Claimed, true, nil
}

// MarkSprintRunning PUTs /sprints/{id}/status with the firing run_id (no status
// change — the stories are already running from the claim).
func (p *NativeHTTPProvider) MarkSprintRunning(ctx context.Context, sprintID, runID string) error {
	return p.putSprintStatus(ctx, sprintID, storyStatusReq{RunID: runID})
}

// MarkSprintInReview PUTs /sprints/{id}/status with status=in_review and the shared PR URL.
func (p *NativeHTTPProvider) MarkSprintInReview(ctx context.Context, sprintID, prURL string) error {
	return p.putSprintStatus(ctx, sprintID, storyStatusReq{Status: "in_review", PRURL: prURL})
}

// MarkSprintDone PUTs /sprints/{id}/status with status=done.
func (p *NativeHTTPProvider) MarkSprintDone(ctx context.Context, sprintID string) error {
	return p.putSprintStatus(ctx, sprintID, storyStatusReq{Status: "done"})
}

// MarkSprintFailed PUTs /sprints/{id}/status with status=failed.
func (p *NativeHTTPProvider) MarkSprintFailed(ctx context.Context, sprintID string) error {
	return p.putSprintStatus(ctx, sprintID, storyStatusReq{Status: "failed"})
}

func (p *NativeHTTPProvider) putSprintStatus(ctx context.Context, sprintID string, payload storyStatusReq) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		p.baseURL+"/sprints/"+sprintID+"/status", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("put /sprints/%s/status: %w", sprintID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("put /sprints/%s/status: status %d", sprintID, resp.StatusCode)
	}
	return nil
}

func (p *NativeHTTPProvider) putStatus(ctx context.Context, id string, payload storyStatusReq) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		p.baseURL+"/stories/"+id+"/status", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("put /stories/%s/status: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("put /stories/%s/status: status %d", id, resp.StatusCode)
	}
	return nil
}

// NativeScheduler polls the native ticket store (via StoryProvider), fires
// control-plane runs for ready stories, and marks them done when their runs
// complete. It shares the same ControlPlane interface as Orchestrator so both
// paths fire runs the same way.
//
// Idempotency: no state file is needed — MarkRunning flips the story status to
// "running", which removes it from Ready() on the next cycle. Restarting the
// scheduler simply re-polls; stories already running are tracked via Running().
type NativeScheduler struct {
	provider StoryProvider
	cp       ControlPlane
	workflow string
	gh       MergeChecker
}

// MergeChecker is the GitHub surface the merge-reconcile loop needs: check whether
// a PR is merged, and (in auto mode) merge it. *github.Client satisfies this; tests
// use a fake. A nil MergeChecker disables the merge loop (work stays in_review).
type MergeChecker interface {
	PRMerged(ctx context.Context, repoURL string, number int) (bool, error)
	MergePR(ctx context.Context, repoURL string, number int) error
}

// NewNativeScheduler builds a NativeScheduler. gh may be nil to disable the
// merge-reconcile loop (e.g. local PR_MODE where there are no GitHub PRs).
func NewNativeScheduler(provider StoryProvider, cp ControlPlane, workflow string, gh MergeChecker) *NativeScheduler {
	return &NativeScheduler{provider: provider, cp: cp, workflow: workflow, gh: gh}
}

// terminalFailed reports whether a run status is a terminal failure — the run
// has ended and will never succeed. Distinct from still-in-progress statuses.
func terminalFailed(status string) bool {
	switch status {
	case "FAILED", "CANCELLED":
		return true
	}
	return false
}

// RunOnce executes a single poll-and-fire cycle. It reads execution_unit from the
// control plane and dispatches to sprint-batched (goal) mode or per-story mode:
//
//   - "sprint" (default): a whole sprint is implemented in ONE run on ONE branch
//     → ONE PR. ReadySprints are claimed atomically and fired with a combined
//     "goal mode" ticket; a sprint run's completion advances ALL its stories.
//   - "story": one run/PR per story (the original behavior).
//
// execution_unit is read fresh each cycle so the toggle takes effect without a
// restart; an unreadable setting falls back to sprint (the default).
// RunOnce returns the number of actions taken so the loop can reset its backoff.
func (s *NativeScheduler) RunOnce(ctx context.Context) (int, error) {
	unit, err := s.cp.ExecutionUnit(ctx)
	if err != nil {
		log.Printf("native-scheduler: read execution_unit (defaulting to sprint): %v", err)
		unit = "sprint"
	}
	if unit == "" {
		unit = "sprint"
	}

	// Merge-reconcile first: advance any in_review work whose PR has merged (and,
	// in auto mode, merge the PRs ourselves) BEFORE firing, so a dependent can
	// unblock in the same cycle its prerequisite's PR lands.
	actions := s.reconcileMerges(ctx)

	var fired int
	if unit == "story" {
		fired, err = s.runStoryMode(ctx)
	} else {
		fired, err = s.runSprintMode(ctx)
	}
	return actions + fired, err
}

// reconcileMerges advances in_review work toward done based on its PR's merge
// state. Each cycle, for every in_review story (grouped so a goal-mode sprint is
// handled once), it:
//   - checks whether the recorded PR is MERGED → marks done (unblocking dependents);
//   - if merge_mode == "auto" AND the PR is still open, MERGES it (the run already
//     passed gate + review) and marks done in the same cycle.
//
// merge_mode is read fresh from the control plane each cycle (like execution_unit),
// so the toggle takes effect without a restart. A nil gh client or an unrecorded PR
// leaves the work in_review (manual-merge-by-human is then the only path). Errors
// are logged and skipped — the work simply waits for the next cycle.
func (s *NativeScheduler) reconcileMerges(ctx context.Context) int {
	if s.gh == nil {
		return 0
	}
	inReview, err := s.provider.InReview(ctx)
	if err != nil {
		log.Printf("native-scheduler: list in_review: %v", err)
		return 0
	}
	if len(inReview) == 0 {
		return 0
	}
	mode, err := s.cp.MergeMode(ctx)
	if err != nil {
		log.Printf("native-scheduler: read merge_mode (defaulting to manual): %v", err)
		mode = "manual"
	}
	if mode == "" {
		mode = "manual"
	}

	actions := 0
	seenSprint := map[string]bool{} // a goal-mode sprint shares one PR — reconcile it once
	for _, t := range inReview {
		if t.SprintID != "" {
			if seenSprint[t.SprintID] {
				continue
			}
			seenSprint[t.SprintID] = true
		}
		if s.reconcileOne(ctx, t, mode) {
			actions++
		}
	}
	return actions
}

// reconcileOne reconciles a single in_review unit (a loose story, or one sprint
// represented by any of its stories). Returns true if it advanced the unit to done.
func (s *NativeScheduler) reconcileOne(ctx context.Context, t NativeTicket, mode string) bool {
	if t.PRURL == "" {
		return false // no PR recorded yet — nothing to check
	}
	number, ok := prNumberFromURL(t.PRURL)
	if !ok {
		log.Printf("native-scheduler: cannot parse PR number from %q (story=%s)", t.PRURL, t.ID)
		return false
	}

	merged, err := s.gh.PRMerged(ctx, t.Repo, number)
	if err != nil {
		log.Printf("native-scheduler: PR merged check story=%s pr=%d: %v", t.ID, number, err)
		return false
	}
	if !merged {
		if mode != "auto" {
			return false // manual mode — wait for a human to merge on GitHub
		}
		// Auto mode: the run already passed gate + review, so merge it ourselves.
		if err := s.gh.MergePR(ctx, t.Repo, number); err != nil {
			log.Printf("native-scheduler: auto-merge story=%s pr=%d: %v", t.ID, number, err)
			return false
		}
		log.Printf("native-scheduler: auto-merged story=%s pr=%d", t.ID, number)
	}
	// Merged (just now in auto mode, or by a human in manual mode) → advance to done.
	return s.markMerged(ctx, t)
}

// markMerged advances a reconciled unit to done: the whole sprint for a goal-mode
// story, or the single story otherwise. Returns true on success.
func (s *NativeScheduler) markMerged(ctx context.Context, t NativeTicket) bool {
	if t.SprintID != "" {
		if err := s.provider.MarkSprintDone(ctx, t.SprintID); err != nil {
			log.Printf("native-scheduler: mark sprint done sprint=%s: %v", t.SprintID, err)
			return false
		}
		log.Printf("native-scheduler: sprint %s PR merged — all stories done", t.SprintID)
		return true
	}
	if err := s.provider.MarkDone(ctx, t.ID); err != nil {
		log.Printf("native-scheduler: mark done story=%s: %v", t.ID, err)
		return false
	}
	log.Printf("native-scheduler: story %s PR merged — done", t.ID)
	return true
}

// prNumberFromURL extracts the PR number from a GitHub PR URL, e.g.
// "https://github.com/acme/widgets/pull/42" → 42. Returns ok=false if the URL has
// no "/pull/<n>" segment or the number does not parse.
func prNumberFromURL(prURL string) (int, bool) {
	const marker = "/pull/"
	i := strings.Index(prURL, marker)
	if i < 0 {
		return 0, false
	}
	rest := prURL[i+len(marker):]
	// Trim anything after the number (e.g. "/files", "#discussion", a trailing /).
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// runStoryMode is the per-story poll-and-fire cycle:
//
//  1. For each running story: check its run's status; if DONE, mark the story
//     done; if terminal-failed (FAILED/CANCELLED), mark it failed. Stories whose
//     run is still in progress (RUNNING/QUEUED/AWAITING) are skipped.
//  2. For each ready story: CLAIM it first (atomic backlog→running), then fire a
//     run. If the claim returns false (another scheduler got it), skip — this
//     prevents double-fire under concurrent schedulers.
//
// Completing a story advances its status to "done", which unblocks any
// dependent stories — they will appear in Ready() on the next cycle.
func (s *NativeScheduler) runStoryMode(ctx context.Context) (int, error) {
	actions := 0
	// Step (a) — advance completions before firing so a dep can unblock in the
	// same cycle that its run finishes (matches the GitHub orchestrator's order).
	running, err := s.provider.Running(ctx)
	if err != nil {
		return actions, fmt.Errorf("list running stories: %w", err)
	}
	for _, t := range running {
		if t.RunID == "" {
			continue
		}
		status, err := s.cp.RunStatus(ctx, t.RunID)
		if err != nil {
			log.Printf("native-scheduler: run status story=%s run=%s: %v", t.ID, t.RunID, err)
			continue
		}
		if status == "DONE" {
			// Run finished → PR is OPEN, not merged. Move to in_review and record the
			// PR so the merge-reconcile loop advances it to done once it merges. The
			// dependent stays blocked until then (Ready gates on done).
			prURL, _ := s.cp.RunPRURL(ctx, t.RunID)
			if err := s.provider.MarkInReview(ctx, t.ID, prURL); err != nil {
				log.Printf("native-scheduler: mark in_review story=%s: %v", t.ID, err)
				continue
			}
			actions++
			log.Printf("native-scheduler: story %s run %s DONE — in_review (pr=%s)", t.ID, t.RunID, prURL)
			continue
		}
		if terminalFailed(status) {
			if err := s.provider.MarkFailed(ctx, t.ID); err != nil {
				log.Printf("native-scheduler: mark failed story=%s: %v", t.ID, err)
				continue
			}
			actions++
			log.Printf("native-scheduler: story %s run %s %s — marked failed", t.ID, t.RunID, status)
		}
		// In-progress statuses (RUNNING, QUEUED, AWAITING, …): leave it alone.
	}

	// Step (b) — fire ready stories. Claim-then-fire to prevent double-fire.
	ready, err := s.provider.Ready(ctx)
	if err != nil {
		return actions, fmt.Errorf("list ready stories: %w", err)
	}
	for _, t := range ready {
		// Atomically claim backlog → running before firing. If another scheduler
		// (or a previous cycle that hasn't flushed yet) already claimed it, skip.
		claimed, err := s.provider.Claim(ctx, t.ID)
		if err != nil {
			log.Printf("native-scheduler: claim story=%s: %v", t.ID, err)
			continue
		}
		if !claimed {
			log.Printf("native-scheduler: story %s already claimed — skipping", t.ID)
			continue
		}

		// Build a rich ticket from the full story (title + body + acceptance) so the
		// implementing agent has the complete context, and a short `title` so the UI
		// doesn't show the whole description as the run title.
		// repo is passed through so OnSeed can clone the project repository.
		// agent is the story's owner — lane routing: the runner uses it to pick the
		// per-lane specialist (python-dev, react-dev…); empty falls back to "dev".
		title, ticket, repo, agent := t.Title, t.Title, "", ""
		if st, gerr := s.provider.GetStory(ctx, t.ID); gerr == nil {
			title = st.Title
			ticket = st.Title
			if st.Body != "" {
				ticket += "\n\n" + st.Body
			}
			if st.Accept != "" {
				ticket += "\n\nAcceptance criteria:\n" + st.Accept
			}
			repo = st.Repo
			agent = st.Owner
		}
		payload := map[string]any{
			"story_id": t.ID,
			"title":    title,
			"ticket":   ticket,
			"repo":     repo,
			"agent":    agent,
		}
		runID, err := s.cp.FireRun(ctx, s.workflow, payload)
		if err != nil {
			log.Printf("native-scheduler: fire run story=%s: %v", t.ID, err)
			continue
		}
		// Record the run_id on the (already-running) story.
		if err := s.provider.MarkRunning(ctx, t.ID, runID); err != nil {
			log.Printf("native-scheduler: mark running story=%s run=%s: %v", t.ID, runID, err)
			continue
		}
		actions++
		log.Printf("native-scheduler: story %s claimed and fired — run %s", t.ID, runID)
	}
	return actions, nil
}

// sprintPreamble is the "goal mode" header prepended to a sprint's combined
// ticket. It tells the implementing agent the contract of a single-run sprint:
// implement every story in order on one branch, the gate runs the full suite,
// and the result is one PR for the whole sprint.
const sprintPreamble = `# Goal mode — implement this entire sprint in ONE pass

You are implementing a WHOLE SPRINT in a single run, on a single branch, that
will become ONE pull request for the whole sprint. Work through every story
below IN THE ORDER GIVEN — they are listed in dependency order, so each story
may build on the ones before it. Do not open separate branches or PRs per story.

Rules:
  - Implement ALL of the stories below; do not stop after the first one.
  - Respect the given order: a later story may depend on an earlier one.
  - The gate runs the FULL test suite over the combined change; make every
    story's acceptance criteria pass together before you finish.
  - The whole sprint ships as a SINGLE PR — keep the change coherent.

Stories (in order):
`

// runSprintMode is the goal-mode poll-and-fire cycle. It fires a whole sprint as
// ONE run and advances all of a sprint's stories together:
//
//  1. Completion: group running stories by sprint_id; for each sprint with a
//     recorded run_id, check the run. DONE → MarkSprintDone (all stories done);
//     FAILED/CANCELLED → MarkSprintFailed. Stories without a sprint (story-mode
//     leftovers) are advanced individually so a mid-flight mode switch settles.
//  2. Firing: for each ReadySprint, ClaimSprint atomically (all-backlog→running);
//     if the claim is lost to a concurrent scheduler, skip. Otherwise build ONE
//     combined goal-mode ticket from the topo-ordered stories and FireRun once.
func (s *NativeScheduler) runSprintMode(ctx context.Context) (int, error) {
	actions := 0

	// Step (a) — advance completions, grouping running stories by sprint.
	running, err := s.provider.Running(ctx)
	if err != nil {
		return actions, fmt.Errorf("list running stories: %w", err)
	}
	a, err := s.advanceRunningSprints(ctx, running)
	if err != nil {
		return actions, err
	}
	actions += a

	// Step (b) — fire ready sprints. Claim-then-fire to prevent double-fire.
	sprints, err := s.provider.ReadySprints(ctx)
	if err != nil {
		return actions, fmt.Errorf("list ready sprints: %w", err)
	}
	for _, sp := range sprints {
		if s.fireSprint(ctx, sp) {
			actions++
		}
	}
	return actions, nil
}

// advanceRunningSprints groups running stories by sprint_id and, for each sprint
// whose shared run has reached a terminal state, advances ALL its stories at once.
// Stories with no sprint_id are advanced individually (story-mode carryover after
// a mode switch) so nothing gets stuck. Returns the number of advance actions.
func (s *NativeScheduler) advanceRunningSprints(ctx context.Context, running []NativeTicket) (int, error) {
	actions := 0
	// runID per sprint (all stories of a goal-mode sprint share one run_id).
	sprintRun := map[string]string{}
	for _, t := range running {
		if t.SprintID == "" {
			actions += s.advanceLooseStory(ctx, t)
			continue
		}
		if t.RunID != "" {
			sprintRun[t.SprintID] = t.RunID
		}
	}
	for sprintID, runID := range sprintRun {
		status, err := s.cp.RunStatus(ctx, runID)
		if err != nil {
			log.Printf("native-scheduler: run status sprint=%s run=%s: %v", sprintID, runID, err)
			continue
		}
		if status == "DONE" {
			// Run finished → the sprint's single PR is OPEN. Move all its stories to
			// in_review and record the shared PR; the reconcile loop marks them done
			// once that PR merges. Dependents of the sprint stay blocked until then.
			prURL, _ := s.cp.RunPRURL(ctx, runID)
			if err := s.provider.MarkSprintInReview(ctx, sprintID, prURL); err != nil {
				log.Printf("native-scheduler: mark sprint in_review sprint=%s: %v", sprintID, err)
				continue
			}
			actions++
			log.Printf("native-scheduler: sprint %s run %s DONE — in_review (pr=%s)", sprintID, runID, prURL)
			continue
		}
		if terminalFailed(status) {
			if err := s.provider.MarkSprintFailed(ctx, sprintID); err != nil {
				log.Printf("native-scheduler: mark sprint failed sprint=%s: %v", sprintID, err)
				continue
			}
			actions++
			log.Printf("native-scheduler: sprint %s run %s %s — all stories marked failed", sprintID, runID, status)
		}
		// In-progress: leave the sprint running.
	}
	return actions, nil
}

// advanceLooseStory advances a single running story that has no sprint (a
// story-mode leftover seen while in sprint mode). Returns 1 if it acted.
func (s *NativeScheduler) advanceLooseStory(ctx context.Context, t NativeTicket) int {
	if t.RunID == "" {
		return 0
	}
	status, err := s.cp.RunStatus(ctx, t.RunID)
	if err != nil {
		log.Printf("native-scheduler: run status story=%s run=%s: %v", t.ID, t.RunID, err)
		return 0
	}
	if status == "DONE" {
		// Loose story (story-mode leftover): same merge-gated path — in_review, not done.
		prURL, _ := s.cp.RunPRURL(ctx, t.RunID)
		if err := s.provider.MarkInReview(ctx, t.ID, prURL); err != nil {
			log.Printf("native-scheduler: mark in_review story=%s: %v", t.ID, err)
			return 0
		}
		return 1
	}
	if terminalFailed(status) {
		if err := s.provider.MarkFailed(ctx, t.ID); err != nil {
			log.Printf("native-scheduler: mark failed story=%s: %v", t.ID, err)
			return 0
		}
		return 1
	}
	return 0
}

// fireSprint claims a sprint atomically and, on success, fires ONE goal-mode run
// for the whole sprint. Returns true if a run was fired. A lost claim (concurrent
// scheduler), an empty story set, or any error short-circuits to false.
func (s *NativeScheduler) fireSprint(ctx context.Context, sp NativeSprint) bool {
	// ClaimSprint returns the claimed IDs in topo order, but we re-fetch the full
	// stories (with body/accept/repo) below to build the ticket, so the IDs from
	// the claim aren't needed here — only that the claim was won.
	_, ok, err := s.provider.ClaimSprint(ctx, sp.ID)
	if err != nil {
		log.Printf("native-scheduler: claim sprint=%s: %v", sp.ID, err)
		return false
	}
	if !ok {
		log.Printf("native-scheduler: sprint %s already claimed — skipping", sp.ID)
		return false
	}

	stories, err := s.provider.SprintStories(ctx, sp.ID)
	if err != nil || len(stories) == 0 {
		log.Printf("native-scheduler: sprint %s stories unavailable (%v) — marking failed", sp.ID, err)
		_ = s.provider.MarkSprintFailed(ctx, sp.ID)
		return false
	}

	title := sp.Name
	if title == "" {
		title = sp.ID
	}
	ticket := sprintPreamble + renderSprintStories(stories)
	repo := ""
	storyIDs := make([]string, len(stories))
	for i, st := range stories {
		storyIDs[i] = st.ID
		if repo == "" {
			repo = st.Repo // all stories in a sprint share one repo
		}
	}

	// Lane routing for a sprint: a mono-lane sprint shares one owner, fired as that
	// specialist. A MIXED-owner sprint is a B2 concern (per-lane sub-batching) — for
	// now fall back to "" (the runner defaults to "dev") and warn.
	agent := commonOwner(stories, sp.ID)

	payload := map[string]any{
		"sprint_id": sp.ID,
		"story_ids": storyIDs,
		"title":     title,
		"ticket":    ticket,
		"repo":      repo,
		"agent":     agent,
	}
	runID, err := s.cp.FireRun(ctx, s.workflow, payload)
	if err != nil {
		log.Printf("native-scheduler: fire run sprint=%s: %v", sp.ID, err)
		return false
	}
	if err := s.provider.MarkSprintRunning(ctx, sp.ID, runID); err != nil {
		log.Printf("native-scheduler: mark sprint running sprint=%s run=%s: %v", sp.ID, runID, err)
		return false
	}
	log.Printf("native-scheduler: sprint %s claimed and fired (%d stories) — run %s", sp.ID, len(stories), runID)
	return true
}

// commonOwner returns the single owner shared by every story in a mono-lane
// sprint. If the stories have MIXED owners (a multi-lane sprint), it returns ""
// and logs a warning — the runner then defaults to "dev". Stories with an empty
// owner are treated as "dev" for the purpose of agreement, so a sprint of all
// unowned stories resolves to "" (default) without a spurious mixed warning.
// Mixed-lane sub-batching is deferred to Phase B2.
func commonOwner(stories []NativeStory, sprintID string) string {
	owner := ""
	for i, st := range stories {
		o := st.Owner
		if i == 0 {
			owner = o
			continue
		}
		if o != owner {
			log.Printf("native-scheduler: sprint %s has mixed owners (%q vs %q) — defaulting agent to dev (B2: per-lane sub-batching)",
				sprintID, owner, o)
			return ""
		}
	}
	return owner
}

// renderSprintStories renders each story as a goal-mode section in the order
// given (already topo-sorted): "### <id> — <title>\n<body>\n\nAcceptance
// criteria:\n<accept>". Sections are separated by a blank line.
func renderSprintStories(stories []NativeStory) string {
	var b bytes.Buffer
	for _, st := range stories {
		fmt.Fprintf(&b, "### %s — %s\n", st.ID, st.Title)
		if st.Body != "" {
			b.WriteString(st.Body)
			b.WriteString("\n")
		}
		if st.Accept != "" {
			b.WriteString("\nAcceptance criteria:\n")
			b.WriteString(st.Accept)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Run loops forever, calling RunOnce at interval until ctx is cancelled.
// It backs off (doubles the sleep, up to 5× base) when an idle cycle fires
// nothing new, matching the Orchestrator.Run backoff pattern.
func (s *NativeScheduler) Run(ctx context.Context, base time.Duration) {
	maxInterval := base * 5
	interval := base
	for {
		actions, err := s.RunOnce(ctx)
		if err != nil {
			log.Printf("native-scheduler: poll error: %v", err)
		}
		// Reset to the base interval whenever there was activity (a story fired or
		// completed) so follow-up work — completions, unblocked dependents — is
		// picked up promptly; back off only when idle.
		if actions > 0 {
			interval = base
		} else if interval < maxInterval {
			interval *= 2
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
