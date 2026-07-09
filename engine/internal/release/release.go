// Package release implements the `release` step type: it builds a project's branch
// and publishes a navigable preview, returning a preview_url addressable downstream
// as $<step>.output.preview_url (the future sprint-review reads it). It is NOT a CD
// pipeline — there is no prod deploy, no rollback, no semver tagging.
//
// SECURITY INVARIANT (audit C2, non-negotiable): untrusted repo code must never run
// on the host. Cloning a branch only FETCHES data (git executes no repo-provided
// code — hooks are not transferred by clone), so it happens on the host like the pr
// step's git. But the BUILD (npm/flutter/firebase-cli) runs repo-defined scripts, so
// it runs INSIDE the docker sandbox with the egress allowlist, honoring RequireDocker
// exactly like the agent runner: when docker is required but unavailable the step
// FAILS with a clear detail — it never degrades to running the build on the host.
package release

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"forge/internal/projects"
	"forge/internal/sandbox"
	"forge/internal/workflow"
)

const (
	defaultBranch           = "dev"
	defaultBuildTimeout     = 10 * time.Minute
	defaultMaxArtifactBytes = int64(200) << 20 // 200 MB
	defaultKeepPreviews     = 5
)

// projectStore is the slice of the project store the runner needs: resolve a
// project's release target + firebase token + repo. Satisfied by *projects.Store;
// an interface so tests can stub it and the runner tolerates a nil store.
type projectStore interface {
	Get(id string) (projects.Project, error)
}

// Runner implements workflow.Runner for `release` steps.
type Runner struct {
	// Projects resolves per-project config (release_target, firebase_token, repo).
	// May be nil — the runner then relies purely on step inputs.
	Projects projectStore
	// PreviewsRoot is the directory static artifacts are copied to; the control
	// plane serves it under /previews/{project_id}/{run_id}/. Must match the API
	// server's PreviewsRoot. Empty disables the static target.
	PreviewsRoot string
	// BaseURL is the public origin the preview_url is built on ("" → a root-relative
	// path, which the console resolves against the control plane).
	BaseURL string
	// SandboxTemplate is the docker sandbox config for the build — Image, Network
	// (egress allowlist, NOT none: the build needs npm/firebase), and RequireDocker.
	// Wired identically to the agent runner's template.
	SandboxTemplate sandbox.Config
	// BuildTimeout caps a single build/deploy (0 → 10m).
	BuildTimeout time.Duration
	// MaxArtifactBytes caps a published static artifact (0 → 200MB). Exceeded = fail.
	MaxArtifactBytes int64
	// KeepPreviews is how many previews to retain per project; older ones are GC'd on
	// publish (0 → 5). "disk-full = outage" is an audit risk, so this is not optional.
	KeepPreviews int
	// Clone clones repo@branch into dst (returning combined output). Injectable for
	// tests; nil → gitClone (real `git clone` on the host).
	Clone func(ctx context.Context, repo, branch, dst string) (string, error)
	// SecretSink registers a discovered secret (the firebase token) with the
	// live-log redactor. Optional; nil → no-op.
	SecretSink func(string)
}

// Run implements workflow.Runner. Inputs: branch (default "dev"), target
// (static|firebase; default the project's release_target), repo, project_id.
func (r *Runner) Run(ctx context.Context, _ workflow.Step, inputs map[string]any, workdir string) (workflow.StepResult, error) {
	runID := filepath.Base(workdir)
	projectID := asStr(inputs["project_id"])
	branch := asStr(inputs["branch"])
	if branch == "" {
		branch = defaultBranch
	}
	target := asStr(inputs["target"])
	repo := asStr(inputs["repo"])
	firebaseToken := ""

	// Project config fills what the step inputs didn't specify (inputs win).
	if r.Projects != nil && projectID != "" {
		if p, err := r.Projects.Get(projectID); err == nil {
			if target == "" {
				target = p.ReleaseTarget
			}
			if repo == "" {
				repo = p.Repo
			}
			firebaseToken = p.FirebaseToken
		}
	}
	if target == "" {
		target = projects.ReleaseTargetStatic
	}
	if repo == "" {
		return fail("release: no repo to build (set inputs.repo or the project's repo)")
	}

	// Clone the branch on the HOST (data fetch, no repo-code execution) into a
	// sibling of the run workdir, cleaned up afterward.
	srcDir := workdir + ".release-src"
	_ = os.RemoveAll(srcDir)
	defer os.RemoveAll(srcDir)
	clone := r.Clone
	if clone == nil {
		clone = gitClone
	}
	if out, err := clone(ctx, repo, branch, srcDir); err != nil {
		return fail("release: clone %s@%s failed: %v\n%s", repo, branch, err, tail(out))
	}

	switch target {
	case projects.ReleaseTargetFirebase:
		return r.releaseFirebase(ctx, srcDir, projectID, runID, firebaseToken)
	case projects.ReleaseTargetStatic:
		return r.releaseStatic(ctx, srcDir, projectID, runID)
	default:
		return fail("release: unknown target %q (want %q or %q)", target, projects.ReleaseTargetStatic, projects.ReleaseTargetFirebase)
	}
}

// releaseStatic builds (if needed) and publishes the branch as a served static site.
func (r *Runner) releaseStatic(ctx context.Context, srcDir, projectID, runID string) (workflow.StepResult, error) {
	if r.PreviewsRoot == "" {
		return fail("release(static): no previews directory configured on the control plane")
	}
	plan, err := detectBuild(srcDir)
	if err != nil {
		return fail("release(static): %v", err)
	}

	sandboxKind := ""
	if plan.command != "" {
		// Untrusted build → sandbox (audit C2), honoring RequireDocker.
		out, kind, berr := r.runInSandbox(ctx, srcDir, plan.command)
		sandboxKind = kind
		if berr != nil {
			return fail("release(static): build failed (sandbox=%s): %v\n%s", kind, berr, tail(out))
		}
	}

	outDir, err := plan.resolveOutput(srcDir)
	if err != nil {
		return fail("release(static): %v", err)
	}

	size, err := dirSize(outDir)
	if err != nil {
		return fail("release(static): measure artifact: %v", err)
	}
	if max := r.maxBytes(); max > 0 && size > max {
		return fail("release(static): artifact is %d bytes, over the %d-byte limit", size, max)
	}

	dst := r.previewPath(projectID, runID)
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fail("release(static): create previews dir: %v", err)
	}
	if err := sandbox.CopyTreeNoGit(outDir, dst); err != nil {
		return fail("release(static): publish artifact: %v", err)
	}
	r.gc(projectID)

	url := r.previewURL(projectID, runID)
	detail := fmt.Sprintf("release(static): published %d bytes to %s", size, url)
	if sandboxKind != "" {
		detail += " · build ran in sandbox=" + sandboxKind
	} else {
		detail += " · no build (static files copied)"
	}
	out := map[string]any{
		"preview_url": url,
		"target":      projects.ReleaseTargetStatic,
		"bytes":       size,
		"built":       plan.command != "",
		"sandbox":     sandboxKind,
	}
	return workflow.StepResult{
		Success: true,
		Output:  out,
		Detail:  detail,
		Events:  []workflow.ResultEvent{{Type: "step.release", Data: out}},
	}, nil
}

// releaseFirebase deploys a Firebase Hosting preview channel from inside the sandbox.
func (r *Runner) releaseFirebase(ctx context.Context, srcDir, projectID, runID, token string) (workflow.StepResult, error) {
	if !fileExists(filepath.Join(srcDir, "firebase.json")) {
		return fail("release(firebase): no firebase.json at repo root — cannot deploy a Hosting preview channel")
	}
	if strings.TrimSpace(token) == "" {
		return fail("release(firebase): no firebase token configured for the project")
	}
	if r.SecretSink != nil {
		r.SecretSink(token) // keep it out of any live-log
	}
	channel := "fluxo-" + safeSeg(runID)
	// The CLI (and any build it triggers) run INSIDE the sandbox; the token is
	// injected as env (never in the command string, never logged).
	command := "firebase hosting:channel:deploy " + shellSingleQuote(channel) + " --json --non-interactive"
	out, kind, err := r.runInSandbox(ctx, srcDir, command, "FIREBASE_TOKEN="+token)
	if err != nil {
		return fail("release(firebase): deploy failed (sandbox=%s): %v\n%s", kind, err, tail(redact(out, token)))
	}
	url := parseFirebaseURL(out)
	if url == "" {
		return fail("release(firebase): deploy ran but no channel URL was found in the CLI output")
	}
	result := map[string]any{
		"preview_url": url,
		"target":      projects.ReleaseTargetFirebase,
		"channel":     channel,
		"sandbox":     kind,
	}
	return workflow.StepResult{
		Success: true,
		Output:  result,
		Detail:  "release(firebase): deployed preview channel " + channel + " → " + url,
		Events:  []workflow.ResultEvent{{Type: "step.release", Data: result}},
	}, nil
}

// runInSandbox runs command inside the docker sandbox rooted at dir, honoring
// RequireDocker (audit C2): if docker is required but unavailable it returns the
// ErrDockerRequired error so the caller FAILS the step instead of running on the
// host. extraEnv carries per-run secrets (e.g. the firebase token) as -e values.
func (r *Runner) runInSandbox(ctx context.Context, dir, command string, extraEnv ...string) (output, kind string, err error) {
	cfg := r.SandboxTemplate
	cfg.Workdir = dir
	if cfg.Network == "" {
		cfg.Network = sandbox.DefaultEgressNetwork
	}
	cfg.ExtraEnv = append(append([]string{}, cfg.ExtraEnv...), extraEnv...)
	sb := sandbox.New(cfg)
	if derr := cfg.MustDocker(sb); derr != nil {
		return "", sb.Kind(), derr // refuse to run untrusted build code on the host
	}
	bctx := ctx
	if to := r.buildTimeout(); to > 0 {
		var cancel context.CancelFunc
		bctx, cancel = context.WithTimeout(ctx, to)
		defer cancel()
	}
	out, code, execErr := sb.Exec(bctx, command)
	if execErr != nil {
		return out, sb.Kind(), execErr
	}
	if code != 0 {
		return out, sb.Kind(), fmt.Errorf("exit code %d", code)
	}
	return out, sb.Kind(), nil
}

// buildPlan is how a repo is turned into a servable artifact. command=="" means the
// repo is already static (index.html at root) and is served as-is; otherwise command
// is run in the sandbox and the artifact is the first existing dir in outputs.
type buildPlan struct {
	command string
	outputs []string
}

// detectBuild inspects the repo root and decides how to produce the artifact.
func detectBuild(src string) (buildPlan, error) {
	if fileExists(filepath.Join(src, "package.json")) {
		// npm ci for a clean, lockfile-pinned install; the app's own build script
		// decides framework specifics. dist/ (vite) or build/ (CRA) is the output.
		return buildPlan{command: "npm ci && npm run build", outputs: []string{"dist", "build"}}, nil
	}
	if fileExists(filepath.Join(src, "index.html")) {
		return buildPlan{}, nil // already static; copy the tree
	}
	return buildPlan{}, fmt.Errorf("nothing buildable at repo root: looked for package.json (npm build) and index.html (static site), found neither")
}

// resolveOutput returns the directory to publish: the repo itself for a static tree,
// or the first build output that actually exists.
func (p buildPlan) resolveOutput(src string) (string, error) {
	if len(p.outputs) == 0 {
		return src, nil
	}
	for _, o := range p.outputs {
		if d := filepath.Join(src, o); dirExists(d) {
			return d, nil
		}
	}
	return "", fmt.Errorf("build produced none of the expected output dirs %v", p.outputs)
}

// gc keeps only the newest KeepPreviews previews for a project, deleting older ones
// (the disk is finite — "disk-full = outage" is a flagged audit risk).
func (r *Runner) gc(projectID string) {
	dir := filepath.Join(r.PreviewsRoot, safeSeg(projectID))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type ent struct {
		name string
		mod  time.Time
	}
	var dirs []ent
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, ent{e.Name(), info.ModTime()})
	}
	keep := r.keepPreviews()
	if keep <= 0 || len(dirs) <= keep {
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	for _, d := range dirs[keep:] {
		_ = os.RemoveAll(filepath.Join(dir, d.name))
	}
}

func (r *Runner) previewPath(projectID, runID string) string {
	return filepath.Join(r.PreviewsRoot, safeSeg(projectID), safeSeg(runID))
}

// previewURL is the canonical IDENTIFIER of a published preview (project + run). It is
// not directly openable: the served content is untrusted repo JS, so a preview is
// reached under /pv/{token}/ via a short-lived capability token the console mints
// (POST /projects/{id}/previews/{run}/token) — never a session/service token in the
// URL (audit C2 at serving).
func (r *Runner) previewURL(projectID, runID string) string {
	base := strings.TrimRight(r.BaseURL, "/")
	return base + "/previews/" + safeSeg(projectID) + "/" + safeSeg(runID) + "/"
}

func (r *Runner) buildTimeout() time.Duration {
	if r.BuildTimeout > 0 {
		return r.BuildTimeout
	}
	return defaultBuildTimeout
}

func (r *Runner) maxBytes() int64 {
	if r.MaxArtifactBytes > 0 {
		return r.MaxArtifactBytes
	}
	return defaultMaxArtifactBytes
}

func (r *Runner) keepPreviews() int {
	if r.KeepPreviews > 0 {
		return r.KeepPreviews
	}
	return defaultKeepPreviews
}

// gitClone shallow-clones repo@branch into dst on the host.
func gitClone(ctx context.Context, repo, branch, dst string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--depth", "1",
		"--single-branch", "--branch", branch, repo, dst)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	return buf.String(), cmd.Run()
}

// parseFirebaseURL extracts the preview channel URL from `firebase ... --json`
// output. The JSON is {"result":{"<channel>":{"url":"https://…"}}}; the CLI may
// prepend log lines, so we start at the first '{'. Falls back to scanning for a
// hosting URL if the shape differs.
func parseFirebaseURL(out string) string {
	if i := strings.IndexByte(out, '{'); i >= 0 {
		var doc struct {
			Result map[string]struct {
				URL string `json:"url"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(out[i:]), &doc) == nil {
			for _, ch := range doc.Result {
				if ch.URL != "" {
					return ch.URL
				}
			}
		}
	}
	for _, tok := range strings.Fields(out) {
		t := strings.Trim(tok, `"',`)
		if strings.HasPrefix(t, "https://") && (strings.Contains(t, ".web.app") || strings.Contains(t, ".firebaseapp.com")) {
			return t
		}
	}
	return ""
}

// ---- small helpers ----------------------------------------------------------

func fail(format string, a ...any) (workflow.StepResult, error) {
	return workflow.StepResult{Success: false, Detail: fmt.Sprintf(format, a...)}, nil
}

func asStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// dirSize sums the byte size of all regular files under root.
func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// safeSeg reduces an id to a single safe path/URL segment: no separators, no "..".
// project_id and run_id are kernel-generated slugs, but this defends the previews
// tree (and the served path) against a hand-crafted traversal.
func safeSeg(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, "..", "")
	if s == "" {
		return "_"
	}
	return s
}

// shellSingleQuote wraps s in single quotes, escaping any embedded single quote,
// so it is a single literal argument inside the sandbox's `sh -c`.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// tail returns the last ~2KB of s for a compact failure detail.
func tail(s string) string {
	const max = 2048
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max:]
}

// redact removes secret from s (defense in depth before putting output in a detail).
func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}
