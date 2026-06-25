// Package studio implements the Studio service — the design/spec plane (BMAD-like)
// that turns an idea into a versioned spec and a ready backlog of GitHub Issues.
//
// A Project holds a sequence of Phases: discovery → prd → architecture → ui →
// backlog. Each phase runs a turn of the Engine (real: claude -p --resume; fake:
// deterministic stub), producing/updating a versioned Artifact (.md file) on disk.
//
// Approval gating: a phase advances pending → running → awaiting_approval →
// approved (or rejected → can re-run). The next phase cannot start until the
// current one is approved.
//
// On the final handoff the backlog is published as GitHub Issues via the GitHub
// interface (real: gh issue create; fake: in-memory capture), emitting
// "Depends-on: #N" body lines compatible with the orchestrator's parseDeps.
package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Phases ordered — the canonical pipeline.
var Phases = []string{"discovery", "prd", "architecture", "ui", "backlog"}

// phaseIndex returns the 0-based index of a phase name, or -1 if unknown.
func phaseIndex(name string) int {
	for i, p := range Phases {
		if p == name {
			return i
		}
	}
	return -1
}

// PhaseStatus is the lifecycle state of a single phase within a project.
type PhaseStatus string

const (
	PhaseStatusPending          PhaseStatus = "pending"
	PhaseStatusRunning          PhaseStatus = "running"
	PhaseStatusAwaitingApproval PhaseStatus = "awaiting_approval"
	PhaseStatusApproved         PhaseStatus = "approved"
	PhaseStatusRejected         PhaseStatus = "rejected"
)

// Phase records the state and artifacts of one pipeline phase.
type Phase struct {
	Name      string      `json:"name"`
	Status    PhaseStatus `json:"status"`
	Feedback  string      `json:"feedback,omitempty"`  // human feedback on rejection
	UpdatedAt time.Time   `json:"updated_at"`
	// Artifacts lists artifact names produced by this phase (e.g. "discovery").
	Artifacts []string `json:"artifacts,omitempty"`
}

// Project is a Studio project — one idea being turned into a backlog.
type Project struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Workdir   string            `json:"workdir"`   // directory where artifacts are stored
	Phases    map[string]*Phase `json:"phases"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// currentPhase returns the next phase that is not yet approved, or "" if all done.
func (p *Project) currentPhase() string {
	for _, name := range Phases {
		ph, ok := p.Phases[name]
		if !ok || ph.Status != PhaseStatusApproved {
			return name
		}
	}
	return ""
}

// Artifact holds the versioned content of a phase output.
type Artifact struct {
	ProjectID string    `json:"project_id"`
	Phase     string    `json:"phase"`
	Name      string    `json:"name"` // e.g. "discovery"
	Version   int       `json:"version"`
	Path      string    `json:"path"` // on-disk path to the latest version
	UpdatedAt time.Time `json:"updated_at"`
}

// Engine is the interface through which Studio runs a phase. The real
// implementation shells out to `claude -p --resume`; tests use a fake.
type Engine interface {
	RunPhase(ctx context.Context, workdir, phase, prompt string) (output string, err error)
}

// GitHub is the interface through which Studio creates GitHub Issues on handoff.
// The real implementation shells out to `gh issue create`; tests use a fake.
type GitHub interface {
	CreateIssue(ctx context.Context, title, body string, labels []string) (number int, err error)
}

// BacklogItem represents one issue to be published during handoff.
type BacklogItem struct {
	Title   string
	Body    string   // should contain "Depends-on: #N" lines for deps
	Labels  []string
	DepsOn  []int // issue numbers this item depends on (used to build Depends-on line)
}

// ErrNotFound is returned when a project or artifact cannot be found.
var ErrNotFound = errors.New("not found")

// Studio is the core service — manages projects, phases, artifacts, and handoff.
type Studio struct {
	mu       sync.RWMutex
	root     string            // configurable root (default ./studio-data)
	projects map[string]*Project
	engine   Engine
	gh       GitHub
}

// New creates a Studio rooted at dataRoot. It loads any projects persisted in
// prior runs. engine and gh are the I/O interfaces (real or fake).
func New(dataRoot string, engine Engine, gh GitHub) (*Studio, error) {
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, fmt.Errorf("studio: create data root: %w", err)
	}
	s := &Studio{
		root:     dataRoot,
		projects: make(map[string]*Project),
		engine:   engine,
		gh:       gh,
	}
	if err := s.loadAll(); err != nil {
		return nil, fmt.Errorf("studio: load state: %w", err)
	}
	return s, nil
}

// ---- projects ----------------------------------------------------------------

// CreateProject creates a new project and persists it to disk.
func (s *Studio) CreateProject(id, name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.projects[id]; dup {
		return nil, fmt.Errorf("studio: project %q already exists", id)
	}
	if id == "" || name == "" {
		return nil, errors.New("studio: id and name are required")
	}

	workdir := filepath.Join(s.root, "projects", id)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return nil, fmt.Errorf("studio: create workdir: %w", err)
	}

	now := time.Now().UTC()
	phases := make(map[string]*Phase, len(Phases))
	for _, p := range Phases {
		phases[p] = &Phase{Name: p, Status: PhaseStatusPending, UpdatedAt: now}
	}
	proj := &Project{
		ID:        id,
		Name:      name,
		Workdir:   workdir,
		Phases:    phases,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.projects[id] = proj
	return proj, s.saveProject(proj)
}

// GetProject returns the project with the given id.
func (s *Studio) GetProject(id string) (*Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, fmt.Errorf("project %q: %w", id, ErrNotFound)
	}
	return p, nil
}

// ListProjects returns all projects sorted by creation time.
func (s *Studio) ListProjects() []*Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Project, 0, len(s.projects))
	for _, p := range s.projects {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	return list
}

// ---- phase lifecycle ---------------------------------------------------------

// RunPhase executes a phase for the given project. It enforces gating: a phase
// can only run if all previous phases are approved, or if the phase itself was
// rejected (allowing re-runs). Transitions:
//
//	pending  → (previous all approved) → running → awaiting_approval
//	rejected → running → awaiting_approval
func (s *Studio) RunPhase(ctx context.Context, projectID, phaseName string) error {
	s.mu.Lock()
	proj, ok := s.projects[projectID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("project %q: %w", projectID, ErrNotFound)
	}

	if phaseIndex(phaseName) < 0 {
		s.mu.Unlock()
		return fmt.Errorf("studio: unknown phase %q", phaseName)
	}

	ph := proj.Phases[phaseName]
	if ph.Status == PhaseStatusRunning {
		s.mu.Unlock()
		return fmt.Errorf("studio: phase %q is already running", phaseName)
	}
	if ph.Status == PhaseStatusApproved {
		s.mu.Unlock()
		return fmt.Errorf("studio: phase %q is already approved", phaseName)
	}
	if ph.Status == PhaseStatusAwaitingApproval {
		s.mu.Unlock()
		return fmt.Errorf("studio: phase %q is awaiting approval", phaseName)
	}

	// Gating: all previous phases must be approved before this one can run.
	if ph.Status == PhaseStatusPending {
		idx := phaseIndex(phaseName)
		for _, prev := range Phases[:idx] {
			if proj.Phases[prev].Status != PhaseStatusApproved {
				s.mu.Unlock()
				return fmt.Errorf("studio: phase %q is blocked — %q must be approved first", phaseName, prev)
			}
		}
	}

	// Mark running.
	ph.Status = PhaseStatusRunning
	ph.UpdatedAt = time.Now().UTC()
	proj.UpdatedAt = ph.UpdatedAt
	feedback := ph.Feedback
	workdir := proj.Workdir
	_ = s.saveProject(proj)
	s.mu.Unlock()

	// Build prompt: include any prior rejection feedback.
	prompt := buildPrompt(phaseName, feedback)

	output, err := s.engine.RunPhase(ctx, workdir, phaseName, prompt)

	s.mu.Lock()
	defer s.mu.Unlock()

	proj = s.projects[projectID] // re-fetch after unlock
	ph = proj.Phases[phaseName]
	now := time.Now().UTC()

	if err != nil {
		ph.Status = PhaseStatusRejected
		ph.Feedback = err.Error()
		ph.UpdatedAt = now
		proj.UpdatedAt = now
		_ = s.saveProject(proj)
		return fmt.Errorf("studio: phase %q engine error: %w", phaseName, err)
	}

	// Persist the artifact (versioned).
	artifactName := phaseName
	if err := s.writeArtifact(proj, phaseName, artifactName, output); err != nil {
		ph.Status = PhaseStatusRejected
		ph.Feedback = err.Error()
		ph.UpdatedAt = now
		proj.UpdatedAt = now
		_ = s.saveProject(proj)
		return fmt.Errorf("studio: persist artifact: %w", err)
	}

	ph.Status = PhaseStatusAwaitingApproval
	ph.Feedback = ""
	if !contains(ph.Artifacts, artifactName) {
		ph.Artifacts = append(ph.Artifacts, artifactName)
	}
	ph.UpdatedAt = now
	proj.UpdatedAt = now
	return s.saveProject(proj)
}

// ApprovePhase marks a phase as approved. Returns an error if the phase is not
// awaiting approval.
func (s *Studio) ApprovePhase(projectID, phaseName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	proj, ok := s.projects[projectID]
	if !ok {
		return fmt.Errorf("project %q: %w", projectID, ErrNotFound)
	}
	if phaseIndex(phaseName) < 0 {
		return fmt.Errorf("studio: unknown phase %q", phaseName)
	}

	ph := proj.Phases[phaseName]
	if ph.Status != PhaseStatusAwaitingApproval {
		return fmt.Errorf("studio: phase %q cannot be approved (status=%s)", phaseName, ph.Status)
	}

	now := time.Now().UTC()
	ph.Status = PhaseStatusApproved
	ph.UpdatedAt = now
	proj.UpdatedAt = now
	return s.saveProject(proj)
}

// RejectPhase marks a phase as rejected with optional feedback. The phase can
// be re-run after rejection.
func (s *Studio) RejectPhase(projectID, phaseName, feedback string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	proj, ok := s.projects[projectID]
	if !ok {
		return fmt.Errorf("project %q: %w", projectID, ErrNotFound)
	}
	if phaseIndex(phaseName) < 0 {
		return fmt.Errorf("studio: unknown phase %q", phaseName)
	}

	ph := proj.Phases[phaseName]
	if ph.Status != PhaseStatusAwaitingApproval {
		return fmt.Errorf("studio: phase %q cannot be rejected (status=%s)", phaseName, ph.Status)
	}

	now := time.Now().UTC()
	ph.Status = PhaseStatusRejected
	ph.Feedback = feedback
	ph.UpdatedAt = now
	proj.UpdatedAt = now
	return s.saveProject(proj)
}

// ---- artifacts ---------------------------------------------------------------

// ListArtifacts returns all artifacts for a project.
func (s *Studio) ListArtifacts(projectID string) ([]Artifact, error) {
	s.mu.RLock()
	proj, ok := s.projects[projectID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("project %q: %w", projectID, ErrNotFound)
	}
	return s.scanArtifacts(proj)
}

// GetArtifact returns the latest artifact content for the given name.
func (s *Studio) GetArtifact(projectID, name string) (Artifact, string, error) {
	s.mu.RLock()
	proj, ok := s.projects[projectID]
	s.mu.RUnlock()
	if !ok {
		return Artifact{}, "", fmt.Errorf("project %q: %w", projectID, ErrNotFound)
	}

	art, err := s.latestArtifact(proj, name)
	if err != nil {
		return Artifact{}, "", err
	}
	content, err := os.ReadFile(art.Path)
	if err != nil {
		return Artifact{}, "", fmt.Errorf("read artifact: %w", err)
	}
	return art, string(content), nil
}

// ---- handoff -----------------------------------------------------------------

// Handoff publishes the backlog (final phase "backlog" artifact) as GitHub
// Issues. It reads the BACKLOG.md artifact, parses BacklogItems from it, and
// creates issues in order. Dependency links are emitted as "Depends-on: #N"
// body lines (compatible with the orchestrator's parseDeps) and as
// "depends:#N" labels.
//
// The backlog phase must be approved before handoff can run.
func (s *Studio) Handoff(ctx context.Context, projectID string, items []BacklogItem) ([]int, error) {
	s.mu.RLock()
	proj, ok := s.projects[projectID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("project %q: %w", projectID, ErrNotFound)
	}

	// The backlog phase must be approved.
	s.mu.RLock()
	backlogPhase := proj.Phases["backlog"]
	s.mu.RUnlock()
	if backlogPhase.Status != PhaseStatusApproved {
		return nil, fmt.Errorf("studio: handoff requires backlog phase to be approved (status=%s)", backlogPhase.Status)
	}

	if len(items) == 0 {
		return nil, errors.New("studio: handoff: no backlog items provided")
	}

	// issued[i] = GitHub issue number assigned to items[i] (populated as we go).
	issued := make([]int, len(items))

	for i, item := range items {
		// Build the depends-on body lines from prior-item deps.
		body := buildIssueBody(item, issued)

		// Build labels: existing labels + "depends:#N" for each dep.
		labels := make([]string, len(item.Labels))
		copy(labels, item.Labels)
		for _, dep := range item.DepsOn {
			if dep > 0 && dep <= len(issued) && issued[dep-1] > 0 {
				labels = append(labels, fmt.Sprintf("depends:#%d", issued[dep-1]))
			}
		}

		num, err := s.gh.CreateIssue(ctx, item.Title, body, labels)
		if err != nil {
			return nil, fmt.Errorf("studio: handoff issue %d %q: %w", i+1, item.Title, err)
		}
		issued[i] = num
	}

	return issued, nil
}

// ---- internal helpers -------------------------------------------------------

// buildPrompt produces the prompt for a phase run, incorporating any feedback
// from a prior rejection.
func buildPrompt(phase, feedback string) string {
	desc := phaseDescription(phase)
	if feedback == "" {
		return desc
	}
	return fmt.Sprintf("%s\n\nPrevious run was rejected with feedback:\n%s\n\nPlease address this feedback.", desc, feedback)
}

// phaseDescription returns a short instructional prompt for each phase.
func phaseDescription(phase string) string {
	switch phase {
	case "discovery":
		return "Run the discovery phase: define the problem, target users, and the scope of the v1 solution. Produce a DISCOVERY.md document."
	case "prd":
		return "Run the PRD phase: translate the discovery into detailed product requirements. Produce a PRD.md document."
	case "architecture":
		return "Run the architecture phase: design the technical architecture for the PRD. Produce an ARCHITECTURE.md document."
	case "ui":
		return "Run the UI phase: specify the user interface screens and flows. Produce a UI_SCREENS.md document."
	case "backlog":
		return "Run the backlog phase: decompose the spec into a prioritized backlog of implementation issues. Produce a BACKLOG.md document with YAML frontmatter per issue (id, sprint, wave, owner, depends_on)."
	}
	return fmt.Sprintf("Run phase: %s", phase)
}

// writeArtifact persists content as a versioned artifact file under
// proj.Workdir/artifacts/<name>.v<N>.md, keeping all prior versions, and
// writing a pointer <name>.md → latest version.
func (s *Studio) writeArtifact(proj *Project, phaseName, name, content string) error {
	dir := filepath.Join(proj.Workdir, "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create artifacts dir: %w", err)
	}

	// Determine next version number.
	version := s.nextVersion(dir, name)
	versioned := filepath.Join(dir, fmt.Sprintf("%s.v%d.md", name, version))
	if err := os.WriteFile(versioned, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write versioned artifact: %w", err)
	}

	// Update the "latest" pointer (symlink-free for portability: a plain file).
	latest := filepath.Join(dir, name+".md")
	if err := os.WriteFile(latest, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write latest artifact: %w", err)
	}
	return nil
}

// nextVersion scans dir for existing <name>.v<N>.md files and returns N+1.
func (s *Studio) nextVersion(dir, name string) int {
	prefix := name + ".v"
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	max := 0
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".md") {
			continue
		}
		mid := n[len(prefix) : len(n)-3]
		var v int
		if _, err := fmt.Sscanf(mid, "%d", &v); err == nil && v > max {
			max = v
		}
	}
	return max + 1
}

// scanArtifacts enumerates all artifact files (latest only, not versioned) in
// proj.Workdir/artifacts.
func (s *Studio) scanArtifacts(proj *Project) ([]Artifact, error) {
	dir := filepath.Join(proj.Workdir, "artifacts")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	var arts []Artifact
	for _, e := range ents {
		n := e.Name()
		// Skip versioned files (*.v<N>.md) — only expose latest pointers.
		if !strings.HasSuffix(n, ".md") || containsVersion(n) {
			continue
		}
		name := strings.TrimSuffix(n, ".md")
		path := filepath.Join(dir, n)
		info, err := e.Info()
		if err != nil {
			continue
		}
		version := s.latestVersionNum(dir, name)
		// Find which phase this artifact belongs to.
		phase := phaseForArtifact(name)
		arts = append(arts, Artifact{
			ProjectID: proj.ID,
			Phase:     phase,
			Name:      name,
			Version:   version,
			Path:      path,
			UpdatedAt: info.ModTime(),
		})
	}
	return arts, nil
}

// latestArtifact returns metadata for the latest version of an artifact.
func (s *Studio) latestArtifact(proj *Project, name string) (Artifact, error) {
	dir := filepath.Join(proj.Workdir, "artifacts")
	latest := filepath.Join(dir, name+".md")
	info, err := os.Stat(latest)
	if os.IsNotExist(err) {
		return Artifact{}, fmt.Errorf("artifact %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("stat artifact: %w", err)
	}
	return Artifact{
		ProjectID: proj.ID,
		Phase:     phaseForArtifact(name),
		Name:      name,
		Version:   s.latestVersionNum(dir, name),
		Path:      latest,
		UpdatedAt: info.ModTime(),
	}, nil
}

// latestVersionNum returns the highest version number written for a name.
func (s *Studio) latestVersionNum(dir, name string) int {
	v := s.nextVersion(dir, name) - 1
	if v < 1 {
		return 1
	}
	return v
}

// phaseForArtifact maps an artifact name to its phase.
func phaseForArtifact(name string) string {
	switch name {
	case "discovery":
		return "discovery"
	case "prd":
		return "prd"
	case "architecture":
		return "architecture"
	case "ui", "ui_screens":
		return "ui"
	case "backlog":
		return "backlog"
	}
	return ""
}

// containsVersion returns true when n looks like a versioned artifact file
// (e.g. "discovery.v2.md").
func containsVersion(n string) bool {
	// A versioned file has a ".v<digits>." pattern before ".md".
	base := strings.TrimSuffix(n, ".md")
	dot := strings.LastIndex(base, ".v")
	if dot < 0 {
		return false
	}
	rest := base[dot+2:]
	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(rest) > 0
}

// buildIssueBody assembles the GitHub issue body including Depends-on line.
// issued maps item index (0-based) to the GitHub issue number assigned so far.
func buildIssueBody(item BacklogItem, issued []int) string {
	body := item.Body

	// Collect resolved dep numbers.
	var deps []string
	for _, dep := range item.DepsOn {
		if dep <= 0 || dep > len(issued) {
			continue
		}
		num := issued[dep-1]
		if num > 0 {
			deps = append(deps, fmt.Sprintf("#%d", num))
		}
	}
	if len(deps) == 0 {
		return body
	}
	line := "Depends-on: " + strings.Join(deps, ", ")
	if body == "" {
		return line
	}
	return body + "\n\n" + line
}

// contains checks whether a string slice contains val.
func contains(ss []string, val string) bool {
	for _, s := range ss {
		if s == val {
			return true
		}
	}
	return false
}

// ---- persistence -------------------------------------------------------------

// projectStatePath returns the path to a project's state JSON file.
func (s *Studio) projectStatePath(id string) string {
	return filepath.Join(s.root, "projects", id, "state.json")
}

// saveProject persists a project's state to disk. Caller must hold s.mu (write).
func (s *Studio) saveProject(proj *Project) error {
	b, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal project: %w", err)
	}
	path := s.projectStatePath(proj.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create project dir: %w", err)
	}
	return os.WriteFile(path, b, 0o644)
}

// loadAll scans the root/projects/ directory and loads all persisted projects.
func (s *Studio) loadAll() error {
	dir := filepath.Join(s.root, "projects")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil // no projects yet
	}
	if err != nil {
		return fmt.Errorf("read projects dir: %w", err)
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "state.json")
		b, err := os.ReadFile(path)
		if err != nil {
			continue // skip malformed entries
		}
		var proj Project
		if err := json.Unmarshal(b, &proj); err != nil {
			continue
		}
		s.projects[proj.ID] = &proj
	}
	return nil
}
