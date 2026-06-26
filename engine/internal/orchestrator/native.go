package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NativeTicket is the orchestrator's view of a control-plane story.
type NativeTicket struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	RunID     string   `json:"run_id"`
	Deps      []string `json:"deps"`
	SprintID  string   `json:"sprint_id"`  // empty in story mode; set when the story belongs to a sprint
	PRURL     string   `json:"pr_url"`     // recorded when in_review; the PR the reconcile loop checks for merge
	Repo      string   `json:"repo"`       // the repo the PR lives in (needed to address it via gh)
	ProjectID string   `json:"project_id"` // the project this work belongs to (audit A1) — drives per-project settings
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
	// ResetClaim returns a claimed-but-unfired story (running, no run_id) to backlog
	// so it is retried next cycle. The compensating action for a FireRun that failed
	// AFTER a successful Claim (B3) — without it the story is stranded running with
	// an empty run_id, which the completion loop skips forever.
	ResetClaim(ctx context.Context, id string) error
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
	// ResetSprintClaim returns a sprint's just-claimed (running, no run_id) stories
	// to backlog — the sprint-wide compensating action for a FireRun that failed
	// after ClaimSprint (B3).
	ResetSprintClaim(ctx context.Context, sprintID string) error
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
	ID        string `json:"id"`
	Name      string `json:"name"`
	Goal      string `json:"goal"`
	ProjectID string `json:"project_id"` // the project this sprint belongs to (audit A1)
}

// NativeStory is the full story the scheduler passes to a run as context.
// Repo is the GitHub repository URL the factory agent clones to implement
// the story; it flows from the design run payload → story.repo → run payload.
type NativeStory struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Accept    string `json:"acceptance"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	ProjectID string `json:"project_id"` // the project this story belongs to (audit A1) — stamped on factory runs
}

// NativeHTTPProvider implements StoryProvider against the control-plane HTTP API.
type NativeHTTPProvider struct {
	baseURL string
	http    *http.Client
	token   string // service token sent as "Authorization: Bearer <token>" (C1)
}

// NewNativeHTTPProvider returns a StoryProvider that talks to the control-plane
// at baseURL (e.g. "http://localhost:8080"). Control-plane auth is mandatory:
// VIBEFORGE_API_TOKEN is read from the environment and attached as a Bearer token
// to every request — without it every call 401s. An empty token (auth disabled in
// dev) is sent as no header, matching the cpClient behavior.
func NewNativeHTTPProvider(baseURL string) *NativeHTTPProvider {
	return &NativeHTTPProvider{baseURL: baseURL, http: &http.Client{}, token: os.Getenv("VIBEFORGE_API_TOKEN")}
}

// newReq builds an authenticated request: it sets the Bearer token (when present)
// so every control-plane call the provider makes authenticates. Mirrors
// cpClient.authReq. body may be nil for GETs.
func (p *NativeHTTPProvider) newReq(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	return req, nil
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
	req, err := p.newReq(ctx, http.MethodGet, p.baseURL+"/tickets", nil)
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
	req, err := p.newReq(ctx, http.MethodPost,
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

// ResetClaim PUTs /stories/{id}/status with status=backlog. The store's guarded
// backlog target (MarkBacklog) only resets a running, empty-run_id story, so a
// story that did get a run_id is left running.
func (p *NativeHTTPProvider) ResetClaim(ctx context.Context, id string) error {
	return p.putStatus(ctx, id, storyStatusReq{Status: "backlog"})
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
	req, err := p.newReq(ctx, http.MethodGet, p.baseURL+"/stories/"+id, nil)
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
	req, err := p.newReq(ctx, http.MethodGet, p.baseURL+"/sprints/ready", nil)
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
	req, err := p.newReq(ctx, http.MethodGet, p.baseURL+"/sprints/"+sprintID+"/stories", nil)
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
	req, err := p.newReq(ctx, http.MethodPost, p.baseURL+"/sprints/"+sprintID+"/claim", nil)
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

// ResetSprintClaim PUTs /sprints/{id}/status with status=backlog to reset a
// sprint's just-claimed (running, no run_id) stories after a failed FireRun (B3).
func (p *NativeHTTPProvider) ResetSprintClaim(ctx context.Context, sprintID string) error {
	return p.putSprintStatus(ctx, sprintID, storyStatusReq{Status: "backlog"})
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
	req, err := p.newReq(ctx, http.MethodPut,
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
	req, err := p.newReq(ctx, http.MethodPut,
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

	// mergeFails counts CONSECUTIVE auto-merge failures per in_review unit (story or
	// sprint id). After maxMergeFails the unit is marked failed instead of retrying
	// `gh pr merge` forever on a conflict / branch-protection block (H2). A success
	// clears the counter. Guarded by mu (RunOnce can be called concurrently by
	// multiple schedulers sharing one store).
	mu         sync.Mutex
	mergeFails map[string]int
}

// maxMergeFails bounds consecutive auto-merge attempts before an in_review unit is
// declared failed (H2). A conflict or branch-protection block does not resolve
// itself by retrying, so retrying forever starves the scheduler; fail loudly after
// a few attempts so a human can intervene.
const maxMergeFails = 3

// MergeChecker is the GitHub surface the merge-reconcile loop needs: check whether
// a PR is merged, and (in auto mode) merge it. *github.Client satisfies this; tests
// use a fake. A nil MergeChecker disables the merge loop (work stays in_review).
type MergeChecker interface {
	PRMerged(ctx context.Context, repoURL string, number int) (bool, error)
	MergePR(ctx context.Context, repoURL string, number int) error
}

// PRStateChecker is an OPTIONAL extension a MergeChecker may implement to report a
// PR that a human CLOSED without merging — a terminal state the reconcile loop must
// stop polling (H2). It is consumed via a type assertion so the base MergeChecker
// (and its fakes) need not implement it; when absent, a closed-unmerged PR simply
// keeps waiting (the pre-existing behavior) rather than being detected as terminal.
// The production *github.Client should implement this (gh pr view --json state) —
// FLAGGED for the github lane.
type PRStateChecker interface {
	// PRClosed reports whether the PR is CLOSED and NOT merged (a human rejected it).
	PRClosed(ctx context.Context, repoURL string, number int) (bool, error)
}

// NewNativeScheduler builds a NativeScheduler. gh may be nil to disable the
// merge-reconcile loop (e.g. local PR_MODE where there are no GitHub PRs).
func NewNativeScheduler(provider StoryProvider, cp ControlPlane, workflow string, gh MergeChecker) *NativeScheduler {
	return &NativeScheduler{provider: provider, cp: cp, workflow: workflow, gh: gh, mergeFails: map[string]int{}}
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

// projectMode is a single project's execution settings for one cycle. It is
// fetched once per project (cached in modeCache) so a project's ready work is all
// evaluated under the same execution_unit + merge_mode (audit A2).
type projectMode struct {
	executionUnit string
	mergeMode     string
}

// modeFor returns project's settings, fetching them from the control plane the
// first time and caching them for the rest of this cycle. An unreadable setting
// falls back to the safe defaults (sprint + manual) so a transient API blip does
// not stall or mis-mode a project's work.
func (s *NativeScheduler) modeFor(ctx context.Context, cache map[string]projectMode, projectID string) projectMode {
	if m, ok := cache[projectID]; ok {
		return m
	}
	unit, mode, err := s.cp.ProjectSettings(ctx, projectID)
	if err != nil {
		log.Printf("native-scheduler: read settings for project %q (defaulting sprint/manual): %v", projectID, err)
	}
	if unit == "" {
		unit = "sprint"
	}
	if mode == "" {
		mode = "manual"
	}
	m := projectMode{executionUnit: unit, mergeMode: mode}
	cache[projectID] = m
	return m
}

// RunOnce executes a single poll-and-fire cycle, PER PROJECT (audit A2). Each
// project's ready story/sprint work is evaluated under THAT project's settings:
//
//   - execution_unit "sprint" (default): a whole sprint → ONE run → ONE PR.
//   - execution_unit "story": one run/PR per story.
//   - merge_mode "manual" (default): wait for a human merge; "auto": merge it.
//
// A python project on sprint+manual and a node project on story+auto are both
// handled correctly in the same cycle: work is grouped by project_id, and each
// project's settings are fetched once (modeCache) and applied to its own work.
//
// Settings are read fresh each cycle so a toggle takes effect without a restart.
// RunOnce returns the number of actions taken so the loop can reset its backoff.
func (s *NativeScheduler) RunOnce(ctx context.Context) (int, error) {
	modeCache := map[string]projectMode{}

	// Merge-reconcile first: advance any in_review work whose PR has merged (and,
	// per the project's merge_mode, merge the PRs ourselves) BEFORE firing, so a
	// dependent can unblock in the same cycle its prerequisite's PR lands.
	actions := s.reconcileMerges(ctx, modeCache)

	// Fire ready work, grouped by project so each project runs under its own
	// execution_unit. Both lists are read once and partitioned by project_id.
	fired, err := s.fireByProject(ctx, modeCache)
	return actions + fired, err
}

// fireByProject partitions ready stories and ready sprints by project_id and
// fires each project's work under that project's execution_unit (audit A2). A
// story-mode project fires its ready stories one run each; a sprint-mode project
// fires its ready sprints as goal-mode runs. Completions for running work are
// advanced first (per project, same modes) so dependents unblock promptly.
func (s *NativeScheduler) fireByProject(ctx context.Context, modeCache map[string]projectMode) (int, error) {
	actions := 0

	// Step (a) — advance completions for running work, grouped by project so each
	// project's running stories/sprints settle under its own execution_unit.
	running, err := s.provider.Running(ctx)
	if err != nil {
		return actions, fmt.Errorf("list running stories: %w", err)
	}
	for projectID, rs := range groupByProject(running) {
		mode := s.modeFor(ctx, modeCache, projectID)
		a, err := s.advanceRunning(ctx, rs, mode.executionUnit)
		if err != nil {
			return actions, err
		}
		actions += a
	}

	// Step (b) — fire ready work per project.
	ready, err := s.provider.Ready(ctx)
	if err != nil {
		return actions, fmt.Errorf("list ready stories: %w", err)
	}
	sprints, err := s.provider.ReadySprints(ctx)
	if err != nil {
		return actions, fmt.Errorf("list ready sprints: %w", err)
	}

	readyByProject := groupByProject(ready)
	sprintsByProject := groupSprintsByProject(sprints)

	// The union of project ids that have any ready work this cycle.
	projectIDs := map[string]bool{}
	for pid := range readyByProject {
		projectIDs[pid] = true
	}
	for pid := range sprintsByProject {
		projectIDs[pid] = true
	}

	for pid := range projectIDs {
		mode := s.modeFor(ctx, modeCache, pid)
		if mode.executionUnit == "story" {
			actions += s.fireReadyStories(ctx, readyByProject[pid])
			continue
		}
		// Sprint (goal) mode: fire this project's ready sprints.
		for _, sp := range sprintsByProject[pid] {
			if s.fireSprint(ctx, sp) {
				actions++
			}
		}
	}
	return actions, nil
}

// groupByProject partitions tickets by project_id. An empty project_id (legacy /
// not-yet-backfilled rows) groups under the default project so they still fire.
func groupByProject(tickets []NativeTicket) map[string][]NativeTicket {
	out := map[string][]NativeTicket{}
	for _, t := range tickets {
		pid := t.ProjectID
		if pid == "" {
			pid = "default"
		}
		out[pid] = append(out[pid], t)
	}
	return out
}

// groupSprintsByProject partitions ready sprints by project_id (default for empty).
func groupSprintsByProject(sprints []NativeSprint) map[string][]NativeSprint {
	out := map[string][]NativeSprint{}
	for _, sp := range sprints {
		pid := sp.ProjectID
		if pid == "" {
			pid = "default"
		}
		out[pid] = append(out[pid], sp)
	}
	return out
}

// reconcileMerges advances in_review work toward done based on its PR's merge
// state. Each cycle, for every in_review story (grouped so a goal-mode sprint is
// handled once), it:
//   - checks whether the recorded PR is MERGED → marks done (unblocking dependents);
//   - if merge_mode == "auto" AND the PR is still open, MERGES it (the run already
//     passed gate + review) and marks done in the same cycle.
//
// Each in_review unit is reconciled under ITS OWN project's merge_mode (audit
// A2): a project on "auto" has its reviewed PRs merged by the scheduler while a
// project on "manual" waits for a human — in the same cycle. merge_mode is fetched
// once per project (modeCache) and read fresh each cycle so a toggle takes effect
// without a restart. A nil gh client or an unrecorded PR leaves the work in_review.
// Errors are logged and skipped — the work simply waits for the next cycle.
func (s *NativeScheduler) reconcileMerges(ctx context.Context, modeCache map[string]projectMode) int {
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

	actions := 0
	seenSprint := map[string]bool{} // a goal-mode sprint shares one PR — reconcile it once
	for _, t := range inReview {
		if t.SprintID != "" {
			if seenSprint[t.SprintID] {
				continue
			}
			seenSprint[t.SprintID] = true
		}
		// Apply THIS unit's project merge_mode.
		pid := t.ProjectID
		if pid == "" {
			pid = "default"
		}
		mode := s.modeFor(ctx, modeCache, pid).mergeMode
		if s.reconcileOne(ctx, t, mode) {
			actions++
		}
	}
	return actions
}

// reconcileOne reconciles a single in_review unit (a loose story, or one sprint
// represented by any of its stories). Returns true if it advanced the unit (to
// done on merge, or to failed on a terminal closed PR / exhausted auto-merge).
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
	if merged {
		// Merged (by a human in manual mode, or by us on a prior cycle) → done.
		s.clearMergeFails(unitKey(t))
		return s.markMerged(ctx, t)
	}

	// Not merged. H2: a PR a human CLOSED without merging is TERMINAL — stop polling
	// it forever and mark the unit failed (needs human attention).
	if s.prClosedTerminal(ctx, t, number) {
		log.Printf("native-scheduler: story=%s pr=%d CLOSED unmerged — marking failed (H2)", t.ID, number)
		return s.failUnit(ctx, t)
	}

	if mode != "auto" {
		return false // manual mode — wait for a human to merge on GitHub
	}

	// Auto mode: the run already passed gate + review, so merge it ourselves. A
	// conflict / branch-protection block makes MergePR fail; that does not resolve
	// by retrying, so bound the attempts (H2) and fail the unit once exhausted.
	if err := s.gh.MergePR(ctx, t.Repo, number); err != nil {
		n := s.bumpMergeFails(unitKey(t))
		log.Printf("native-scheduler: auto-merge story=%s pr=%d: %v (attempt %d/%d)", t.ID, number, err, n, maxMergeFails)
		if n >= maxMergeFails {
			log.Printf("native-scheduler: story=%s pr=%d auto-merge exhausted — marking failed (H2)", t.ID, number)
			s.clearMergeFails(unitKey(t))
			return s.failUnit(ctx, t)
		}
		return false
	}
	s.clearMergeFails(unitKey(t))
	log.Printf("native-scheduler: auto-merged story=%s pr=%d", t.ID, number)
	return s.markMerged(ctx, t)
}

// prClosedTerminal reports whether t's PR was CLOSED unmerged — consulted only when
// the gh client implements the optional PRStateChecker (H2). Without it, a closed
// PR is not detected as terminal (the pre-existing wait-forever behavior).
func (s *NativeScheduler) prClosedTerminal(ctx context.Context, t NativeTicket, number int) bool {
	checker, ok := s.gh.(PRStateChecker)
	if !ok {
		return false
	}
	closed, err := checker.PRClosed(ctx, t.Repo, number)
	if err != nil {
		log.Printf("native-scheduler: PR closed check story=%s pr=%d: %v", t.ID, number, err)
		return false
	}
	return closed
}

// failUnit marks a reconciled unit failed: the whole sprint for a goal-mode story,
// or the single story otherwise. Returns true on success.
func (s *NativeScheduler) failUnit(ctx context.Context, t NativeTicket) bool {
	if t.SprintID != "" {
		if err := s.provider.MarkSprintFailed(ctx, t.SprintID); err != nil {
			log.Printf("native-scheduler: mark sprint failed sprint=%s: %v", t.SprintID, err)
			return false
		}
		return true
	}
	if err := s.provider.MarkFailed(ctx, t.ID); err != nil {
		log.Printf("native-scheduler: mark failed story=%s: %v", t.ID, err)
		return false
	}
	return true
}

// unitKey is the merge-failure counter key for an in_review unit: the sprint id for
// a goal-mode story (one shared PR), else the story id.
func unitKey(t NativeTicket) string {
	if t.SprintID != "" {
		return "sprint:" + t.SprintID
	}
	return "story:" + t.ID
}

func (s *NativeScheduler) bumpMergeFails(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergeFails[key]++
	return s.mergeFails[key]
}

func (s *NativeScheduler) clearMergeFails(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mergeFails, key)
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

// prGate inspects a DONE run's pr STEP and decides how its story/sprint should
// advance (H1). It returns the PR url to record and ok=true when the run produced
// a usable result to park in_review; ok=false means the run is DONE but its pr step
// FAILED (or is in an inconsistent state) and the work must be marked FAILED rather
// than parked in_review with an empty PR (which would hang forever).
//
// Reconcile (merge) only runs when a gh client is configured. When gh==nil the
// system never merges (local/no-GitHub mode), so a DONE run with no PR is the
// expected terminal and parking it in_review is correct — prGate then never fails
// on a missing URL. With gh!=nil, a DONE run that produced a pr step which did NOT
// succeed is a genuine failure (the pr step errored), and a DONE run whose pr step
// SUCCEEDED but recorded no GitHub URL is the "no PR to ever merge" hang — both are
// reported as ok=false so the caller fails the work instead of parking it.
func (s *NativeScheduler) prGate(ctx context.Context, runID string) (url string, ok bool) {
	url, prStepOK, hasPR, err := s.cp.RunPRResult(ctx, runID)
	if err != nil {
		// Couldn't inspect the pr step — fall back to the lenient URL probe so a
		// transient API blip doesn't fail a good run; reconcile will retry next cycle.
		log.Printf("native-scheduler: pr-result run=%s: %v (falling back to url probe)", runID, err)
		url, _ = s.cp.RunPRURL(ctx, runID)
		return url, true
	}
	// No merge loop (local mode): no PR is expected — park as-is.
	if s.gh == nil {
		return url, true
	}
	// Merge enabled: a DONE run MUST yield a usable GitHub PR URL. If it doesn't —
	// the pr step failed (!prStepOK), there was no pr step (!hasPR), or the step
	// succeeded but recorded no URL — there is nothing to ever merge, so parking
	// in_review hangs forever (H1). Treat the run as failed/needs-attention.
	if url == "" {
		log.Printf("native-scheduler: run=%s DONE but no usable PR (hasPR=%v prStepOK=%v) — failing (H1)", runID, hasPR, prStepOK)
		return "", false
	}
	return url, true
}

// parkOrFailStory advances a DONE story: park it in_review with its PR (the normal
// path), or — when its pr step failed / produced no mergeable PR (H1) — mark it
// failed instead of hanging. Returns true if it acted.
func (s *NativeScheduler) parkOrFailStory(ctx context.Context, t NativeTicket) bool {
	prURL, ok := s.prGate(ctx, t.RunID)
	if !ok {
		if err := s.provider.MarkFailed(ctx, t.ID); err != nil {
			log.Printf("native-scheduler: mark failed (no PR) story=%s: %v", t.ID, err)
			return false
		}
		log.Printf("native-scheduler: story %s run %s DONE but pr step failed/no-PR — marked failed (H1)", t.ID, t.RunID)
		return true
	}
	if err := s.provider.MarkInReview(ctx, t.ID, prURL); err != nil {
		log.Printf("native-scheduler: mark in_review story=%s: %v", t.ID, err)
		return false
	}
	log.Printf("native-scheduler: story %s run %s DONE — in_review (pr=%s)", t.ID, t.RunID, prURL)
	return true
}

// parkOrFailSprint is parkOrFailStory for a goal-mode sprint: park all its stories
// in_review with the shared PR, or fail the whole sprint when the run's pr step
// produced no mergeable PR (H1). Returns true if it acted.
func (s *NativeScheduler) parkOrFailSprint(ctx context.Context, sprintID, runID string) bool {
	prURL, ok := s.prGate(ctx, runID)
	if !ok {
		if err := s.provider.MarkSprintFailed(ctx, sprintID); err != nil {
			log.Printf("native-scheduler: mark sprint failed (no PR) sprint=%s: %v", sprintID, err)
			return false
		}
		log.Printf("native-scheduler: sprint %s run %s DONE but pr step failed/no-PR — marked failed (H1)", sprintID, runID)
		return true
	}
	if err := s.provider.MarkSprintInReview(ctx, sprintID, prURL); err != nil {
		log.Printf("native-scheduler: mark sprint in_review sprint=%s: %v", sprintID, err)
		return false
	}
	log.Printf("native-scheduler: sprint %s run %s DONE — in_review (pr=%s)", sprintID, runID, prURL)
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

// advanceRunning advances completions for a project's running work under its
// execution_unit (audit A2). A "story"-mode project advances each running story
// individually; a "sprint"-mode project advances by sprint (a sprint's stories
// share one run). The advance order runs BEFORE firing in fireByProject so a dep
// can unblock in the same cycle its run finishes. Returns the number of actions.
func (s *NativeScheduler) advanceRunning(ctx context.Context, running []NativeTicket, executionUnit string) (int, error) {
	if executionUnit == "story" {
		return s.advanceRunningStories(ctx, running), nil
	}
	return s.advanceRunningSprints(ctx, running)
}

// advanceRunningStories is the per-story completion pass: DONE → park in_review
// (or fail if no mergeable PR, H1); FAILED/CANCELLED → failed; a story running
// with an empty run_id is reset to backlog (B4 stranded recovery).
func (s *NativeScheduler) advanceRunningStories(ctx context.Context, running []NativeTicket) int {
	actions := 0
	for _, t := range running {
		if t.RunID == "" {
			// B4: a running story with no run_id never had its run recorded (a
			// FireRun/MarkRunning failure slipped past compensation, or a crash
			// between claim and record). The completion loop can never advance it —
			// recover by resetting it to backlog so it re-fires instead of wedging.
			log.Printf("native-scheduler: story %s running with empty run_id — resetting to backlog (B4)", t.ID)
			if err := s.provider.ResetClaim(ctx, t.ID); err != nil {
				log.Printf("native-scheduler: reset stranded story=%s: %v", t.ID, err)
			} else {
				actions++
			}
			continue
		}
		status, err := s.cp.RunStatus(ctx, t.RunID)
		if err != nil {
			log.Printf("native-scheduler: run status story=%s run=%s: %v", t.ID, t.RunID, err)
			continue
		}
		if status == "DONE" {
			if s.parkOrFailStory(ctx, t) {
				actions++
			}
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
	return actions
}

// fireReadyStories fires a story-mode project's ready stories: CLAIM each
// (atomic backlog→running) then fire a run. A lost claim (concurrent scheduler)
// skips the story — preventing double-fire. The story's project_id is stamped on
// the run payload so the factory run is scoped (audit A1). Returns actions taken.
func (s *NativeScheduler) fireReadyStories(ctx context.Context, ready []NativeTicket) int {
	actions := 0
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
		// project_id flows from the story onto the run so the factory run is scoped.
		title, ticket, repo, agent := t.Title, t.Title, "", ""
		projectID := t.ProjectID
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
			if st.ProjectID != "" {
				projectID = st.ProjectID
			}
		}
		payload := map[string]any{
			"story_id":   t.ID,
			"title":      title,
			"ticket":     ticket,
			"repo":       repo,
			"agent":      agent,
			"project_id": projectID,
		}
		runID, err := s.cp.FireRun(ctx, s.workflow, payload)
		if err != nil {
			// B3: the claim already flipped the story to running. If we leave it
			// there with no run_id, the completion loop skips it forever (empty
			// run_id) — stranded. Compensate by resetting it to backlog so it is
			// re-fired next cycle. If even the reset fails, the reconcile-by-backlog
			// fallback (recoverStrandedRunning) catches it later.
			log.Printf("native-scheduler: fire run story=%s: %v — resetting claim to backlog", t.ID, err)
			if rerr := s.provider.ResetClaim(ctx, t.ID); rerr != nil {
				log.Printf("native-scheduler: reset claim story=%s: %v", t.ID, rerr)
			}
			continue
		}
		// Record the run_id on the (already-running) story. B4: a real run is now
		// executing; if recording its run_id fails the story would be running with no
		// run_id (the run orphaned). Retry once; if it still fails, reset to backlog
		// — the run is lost but the story re-fires rather than wedging forever.
		if err := s.provider.MarkRunning(ctx, t.ID, runID); err != nil {
			log.Printf("native-scheduler: mark running story=%s run=%s: %v — retrying", t.ID, runID, err)
			if err2 := s.provider.MarkRunning(ctx, t.ID, runID); err2 != nil {
				log.Printf("native-scheduler: mark running retry story=%s run=%s: %v — resetting claim", t.ID, runID, err2)
				if rerr := s.provider.ResetClaim(ctx, t.ID); rerr != nil {
					log.Printf("native-scheduler: reset claim story=%s: %v", t.ID, rerr)
				}
				continue
			}
		}
		actions++
		log.Printf("native-scheduler: story %s claimed and fired — run %s", t.ID, runID)
	}
	return actions
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

// advanceRunningSprints groups running stories by sprint_id and, for each sprint
// whose shared run has reached a terminal state, advances ALL its stories at once.
// Stories with no sprint_id are advanced individually (story-mode carryover after
// a mode switch) so nothing gets stuck. Returns the number of advance actions.
func (s *NativeScheduler) advanceRunningSprints(ctx context.Context, running []NativeTicket) (int, error) {
	actions := 0
	// runID per sprint (all stories of a goal-mode sprint share one run_id), plus
	// the set of sprints that have ANY running story so we can detect a sprint that
	// is running with NO run_id at all (B4 stranded after a failed sprint fire).
	sprintRun := map[string]string{}
	sprintSeen := map[string]bool{}
	for _, t := range running {
		if t.SprintID == "" {
			actions += s.advanceLooseStory(ctx, t)
			continue
		}
		sprintSeen[t.SprintID] = true
		if t.RunID != "" {
			sprintRun[t.SprintID] = t.RunID
		}
	}
	// B4: a sprint whose stories are running but carry no run_id was stranded by a
	// failed FireRun/MarkSprintRunning (compensation slipped past, or a crash). It
	// would never advance — reset its just-claimed stories to backlog so it re-fires.
	for sprintID := range sprintSeen {
		if _, fired := sprintRun[sprintID]; fired {
			continue
		}
		log.Printf("native-scheduler: sprint %s running with no run_id — resetting to backlog (B4)", sprintID)
		if err := s.provider.ResetSprintClaim(ctx, sprintID); err != nil {
			log.Printf("native-scheduler: reset stranded sprint=%s: %v", sprintID, err)
		} else {
			actions++
		}
	}
	for sprintID, runID := range sprintRun {
		status, err := s.cp.RunStatus(ctx, runID)
		if err != nil {
			log.Printf("native-scheduler: run status sprint=%s run=%s: %v", sprintID, runID, err)
			continue
		}
		if status == "DONE" {
			// Run finished → the sprint's single PR is OPEN. Park all its stories
			// in_review with the shared PR (reconcile advances them on merge), OR —
			// if the pr step failed / produced no mergeable PR — fail the sprint
			// instead of hanging in_review forever (H1).
			if s.parkOrFailSprint(ctx, sprintID, runID) {
				actions++
			}
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
		// Loose story (story-mode leftover): same merge-gated path — park in_review
		// with its PR, or fail it if its pr step produced no mergeable PR (H1).
		if s.parkOrFailStory(ctx, t) {
			return 1
		}
		return 0
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
	// All stories in a sprint share one repo and one project. Derive both from the
	// stories, falling back to the sprint's own project_id (audit A1).
	projectID := sp.ProjectID
	for i, st := range stories {
		storyIDs[i] = st.ID
		if repo == "" {
			repo = st.Repo
		}
		if projectID == "" {
			projectID = st.ProjectID
		}
	}

	// Lane routing for a sprint: a mono-lane sprint shares one owner, fired as that
	// specialist. A MIXED-owner sprint is a B2 concern (per-lane sub-batching) — for
	// now fall back to "" (the runner defaults to "dev") and warn.
	agent := commonOwner(stories, sp.ID)

	payload := map[string]any{
		"sprint_id":  sp.ID,
		"story_ids":  storyIDs,
		"title":      title,
		"ticket":     ticket,
		"repo":       repo,
		"agent":      agent,
		"project_id": projectID,
	}
	runID, err := s.cp.FireRun(ctx, s.workflow, payload)
	if err != nil {
		// B3: ClaimSprint already flipped every story to running. A failed FireRun
		// would otherwise strand the WHOLE sprint running with empty run_ids
		// (wedged forever). Reset the just-claimed stories to backlog so the sprint
		// is re-fired next cycle.
		log.Printf("native-scheduler: fire run sprint=%s: %v — resetting claim to backlog", sp.ID, err)
		if rerr := s.provider.ResetSprintClaim(ctx, sp.ID); rerr != nil {
			log.Printf("native-scheduler: reset sprint claim sprint=%s: %v", sp.ID, rerr)
		}
		return false
	}
	if err := s.provider.MarkSprintRunning(ctx, sp.ID, runID); err != nil {
		// B4: a real sprint run is executing but its run_id was not recorded. Retry
		// once; on continued failure reset so the (now-stranded) sprint re-fires
		// rather than wedging with empty run_ids.
		log.Printf("native-scheduler: mark sprint running sprint=%s run=%s: %v — retrying", sp.ID, runID, err)
		if err2 := s.provider.MarkSprintRunning(ctx, sp.ID, runID); err2 != nil {
			log.Printf("native-scheduler: mark sprint running retry sprint=%s run=%s: %v — resetting claim", sp.ID, runID, err2)
			if rerr := s.provider.ResetSprintClaim(ctx, sp.ID); rerr != nil {
				log.Printf("native-scheduler: reset sprint claim sprint=%s: %v", sp.ID, rerr)
			}
			return false
		}
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
