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
	Runtime   string   // "docker" | "local"; "" -> auto (docker if available else local)
	Image     string   // docker image (e.g. "python:3.12-slim")
	OCIRuntime string  // set to "runsc" (gVisor) via VIBEFORGE_SANDBOX_RUNTIME
	AllowEnv  []string // env KEY allowlist (DefaultAllowEnv is prepended)
	EgressDeny bool    // true -> --network none (default-deny egress)
	Workdir   string   // host path mounted as the sandbox working tree
}

// Sandbox executes commands in isolation.
type Sandbox interface {
	// Exec runs command (via bash -c) in the sandbox, returning combined output
	// and the process exit code.
	Exec(ctx context.Context, command string) (output string, exitCode int, err error)
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
	image := d.cfg.Image
	if image == "" {
		image = "alpine:3.20"
	}
	args := []string{"run", "--rm"}
	if d.cfg.EgressDeny {
		args = append(args, "--network", "none")
	}
	if d.cfg.OCIRuntime != "" {
		args = append(args, "--runtime", d.cfg.OCIRuntime) // runsc = gVisor
	}
	// Mount the working tree and cd into it.
	args = append(args, "-v", d.cfg.Workdir+":/work", "-w", "/work")
	// Only allowlisted env vars cross, with their values from the daemon env.
	for _, kv := range FilterEnv(os.Environ(), d.cfg.allowSet()) {
		args = append(args, "-e", kv)
	}
	args = append(args, image, "sh", "-c", command)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), exitCodeOf(err), normalizeExecErr(err)
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
