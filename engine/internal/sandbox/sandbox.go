// Package sandbox is a HARD security boundary (non-negotiable per the
// constitution): the LLM's code runs INSIDE; commit/push happen OUTSIDE. It
// provides a per-task isolated execution context — env allowlist (daemon
// secrets never cross), default-deny egress, and a working tree without .git.
//
// Two executors: DockerSandbox (real isolation; gVisor when runsc is configured)
// and LocalSandbox (a scrubbed-env fallback when Docker is absent — NOT a
// security boundary, and it says so). The PURE security functions (env
// allowlist, no-.git copy) are deterministically tested without Docker.
package sandbox

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultAllowEnv is the minimal, secret-free base allowlist. Note what is NOT
// here: GH_TOKEN, ANTHROPIC_API_KEY, AWS_*, DB_*, any credential. Auth that the
// agent legitimately needs is added explicitly per sandbox, never by default.
var DefaultAllowEnv = []string{"PATH", "HOME", "LANG", "LC_ALL", "TERM", "TMPDIR", "USER", "SHELL"}

// Config configures a per-task sandbox.
type Config struct {
	Runtime    string   // "docker" | "local"; "" -> auto (docker if available else local)
	Image      string   // docker image (e.g. "python:3.12-slim")
	OCIRuntime string   // set to "runsc" (gVisor) via VIBEFORGE_SANDBOX_RUNTIME
	AllowEnv   []string // env KEY allowlist (DefaultAllowEnv is prepended)
	EgressDeny bool     // gate: true -> --network none (default-deny egress)
	Workdir    string   // host path mounted as the sandbox working tree

	// Network names the docker network for steps that DO need egress (the agent):
	// a network with NO internet gateway whose only exit is the egress-proxy
	// (allowlist, default-deny). It must NOT be "none" (the agent needs the LLM
	// API) nor the default open "bridge". Ignored when EgressDeny is set (gate).
	Network string
	// UID runs the container as a non-root user "uid:gid" (defense in depth).
	UID string
}

// DefaultEgressNetwork is the per-task agent network (no gateway; only the
// egress-proxy is reachable). Created by the operator / scripts/egress-up.sh.
const DefaultEgressNetwork = "vibeforge-egress"

// Sandbox executes commands in isolation.
type Sandbox interface {
	// Exec runs command (via sh -c) in the sandbox, returning combined output and
	// the process exit code. Used by the GATE (network-denied).
	Exec(ctx context.Context, command string) (output string, exitCode int, err error)

	// WrapAgent turns an agent argv (e.g. `claude -p ...`) into the host command
	// that runs it INSIDE the sandbox, plus the env for that host process. This is
	// the port of v1's `sandbox.wrap(cmd)` + `cli_env()`: the agent's streaming
	// loop is unchanged — only the invoked binary + env change. containerEnv is the
	// egress/auth allowlist that must reach the agent (injected as -e for docker).
	WrapAgent(argv []string, containerEnv []string) (hostArgv []string, hostEnv []string)

	// Kind reports the executor ("docker" or "local").
	Kind() string
}

// FilterEnv returns the subset of parentEnv whose KEY is in the allowlist. This
// is THE function the "no daemon secret crosses to the sandbox" test pins.
func FilterEnv(parentEnv, allow []string) []string {
	allowed := map[string]bool{}
	for _, k := range allow {
		allowed[k] = true
	}
	var out []string
	for _, kv := range parentEnv {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		if allowed[kv[:i]] {
			out = append(out, kv)
		}
	}
	return out
}

// allowSet merges DefaultAllowEnv with the config's extra allowlist.
func (c Config) allowSet() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range append(append([]string{}, DefaultAllowEnv...), c.AllowEnv...) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// New constructs a sandbox per Config, auto-selecting docker when available.
func New(c Config) Sandbox {
	runtime := c.Runtime
	if runtime == "" {
		if dockerAvailable() {
			runtime = "docker"
		} else {
			runtime = "local"
		}
	}
	if runtime == "docker" {
		return &DockerSandbox{cfg: c}
	}
	return &LocalSandbox{cfg: c}
}

func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	cmd := exec.Command("docker", "info")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

// DockerSandbox runs commands via `docker run` with no network (egress-deny),
// the allowlisted env only, and an optional gVisor (runsc) runtime.
type DockerSandbox struct{ cfg Config }

func (d *DockerSandbox) Kind() string { return "docker" }

func (d *DockerSandbox) Exec(ctx context.Context, command string) (string, int, error) {
	args, err := dockerArgs(d.cfg, command, os.Environ())
	if err != nil {
		return "", -1, err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	runErr := cmd.Run()
	return buf.String(), exitCodeOf(runErr), normalizeExecErr(runErr)
}

// dockerArgs builds the `docker run` argument vector (pure, so it is unit
// testable without Docker). The bind mount MUST be an absolute host path —
// Docker treats a relative path like ".vibeforge-runs/x" as a (invalid) named
// volume, not a directory. This is the fix for that footgun.
func dockerArgs(cfg Config, command string, env []string) ([]string, error) {
	image := cfg.Image
	if image == "" {
		image = "alpine:3.20"
	}
	abs, err := filepath.Abs(cfg.Workdir)
	if err != nil {
		return nil, err
	}
	args := []string{"run", "--rm"}
	if cfg.EgressDeny {
		args = append(args, "--network", "none")
	}
	if cfg.OCIRuntime != "" {
		args = append(args, "--runtime", cfg.OCIRuntime) // runsc = gVisor
	}
	args = append(args, "-v", abs+":/work", "-w", "/work")
	for _, kv := range FilterEnv(env, cfg.allowSet()) {
		args = append(args, "-e", kv)
	}
	args = append(args, image, "sh", "-c", command)
	return args, nil
}

// LocalSandbox runs commands locally with a SCRUBBED env (allowlist applied) and
// no network isolation. It is a functional fallback, NOT a security boundary —
// callers must treat docker as the real isolation. Kind() == "local" lets the
// kernel record/flag that a run was not truly sandboxed.
type LocalSandbox struct{ cfg Config }

func (l *LocalSandbox) Kind() string { return "local" }

func (l *LocalSandbox) Exec(ctx context.Context, command string) (string, int, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = l.cfg.Workdir
	cmd.Env = FilterEnv(os.Environ(), l.cfg.allowSet()) // secrets scrubbed even locally
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), exitCodeOf(err), normalizeExecErr(err)
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if ok := asExitError(err, &ee); ok {
		return ee.ExitCode()
	}
	return -1
}

func asExitError(err error, target **exec.ExitError) bool {
	for err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// normalizeExecErr returns nil for a clean exit-code failure (the exit code
// carries that signal) and the real error otherwise (spawn failure, etc.).
func normalizeExecErr(err error) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if asExitError(err, &ee) {
		return nil
	}
	return err
}

// CopyTreeNoGit copies the file tree at src into dst, EXCLUDING the .git
// directory (and dst is created). The agent gets a working tree it cannot use to
// commit/push — git operations happen outside the sandbox, by the kernel.
func CopyTreeNoGit(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		// Skip .git anywhere in the path.
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == ".git" {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// SyncBack propagates the agent's edits from its .git-less worktree (src) back
// into the run worktree (dst), preserving dst's .git. It mirrors v1's
// export_tree: dst is cleared of everything EXCEPT .git (so deletes/renames the
// agent made propagate), then src is copied over. The gate (and pr) then see the
// agent's real result on the run worktree.
func SyncBack(src, dst string) error {
	entries, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue // the run worktree owns git; the agent never touches it
		}
		if err := os.RemoveAll(filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return CopyTreeNoGit(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
