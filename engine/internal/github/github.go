// Package github shells out to the `gh` CLI to manage GitHub repos.
// The host must be authenticated (gh auth login) before use.
// A runner func seam makes the package testable without real gh/git invocations.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Client wraps GitHub operations implemented via the gh/git CLIs.
// runner is injectable for tests; nil uses exec.CommandContext.
type Client struct {
	runner func(ctx context.Context, workdir string, name string, args ...string) (string, error)
}

// New returns a Client that shells out to the real gh/git CLIs.
func New() *Client { return &Client{runner: defaultRunner} }

// withRunner builds a Client with a fake runner (for tests).
func withRunner(r func(ctx context.Context, workdir string, name string, args ...string) (string, error)) *Client {
	return &Client{runner: r}
}

func defaultRunner(ctx context.Context, workdir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// ErrRepoExists is returned by CreateRepo when the repository already exists.
var ErrRepoExists = errors.New("repository already exists")

// CreateRepo creates a GitHub repository org/name and returns its HTTPS URL.
// If the repo already exists it returns ErrRepoExists (not a crash).
func (c *Client) CreateRepo(ctx context.Context, org, name, description string, private bool) (string, error) {
	slug := org + "/" + name
	args := []string{"repo", "create", slug, "--description", description}
	if private {
		args = append(args, "--private")
	} else {
		args = append(args, "--public")
	}
	out, err := c.runner(ctx, "", "gh", args...)
	if err != nil {
		// gh prints "already exists" when the repo is present; surface that clearly.
		if strings.Contains(out, "already exists") || strings.Contains(strings.ToLower(out), "already exists") {
			return "", fmt.Errorf("%w: %s", ErrRepoExists, slug)
		}
		return "", fmt.Errorf("gh repo create %s: %w: %s", slug, err, strings.TrimSpace(out))
	}
	// gh outputs the URL as the last https:// line (or the only line).
	url := lastHTTPS(out)
	if url == "" {
		url = "https://github.com/" + slug
	}
	return url, nil
}

// EnsureDevBranch clones repoURL shallowly into a temp dir, creates a `dev`
// branch off the default branch if it does not exist, pushes it, then removes
// the temp dir. Idempotent: if `dev` already exists on the remote, it succeeds.
func (c *Client) EnsureDevBranch(ctx context.Context, repoURL string) error {
	tmp, err := os.MkdirTemp("", "forge-dev-*")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Shallow clone (depth 1 fetches only the tips; we just need the branch list).
	if out, err := c.runner(ctx, "", "git", "clone", "--depth", "1", "--quiet", repoURL, tmp); err != nil {
		return fmt.Errorf("clone %s: %w: %s", repoURL, err, strings.TrimSpace(out))
	}

	// Check if dev already exists on the remote.
	out, _ := c.runner(ctx, tmp, "git", "ls-remote", "--heads", "origin", "dev")
	if strings.Contains(out, "refs/heads/dev") {
		return nil // already there
	}

	// Create and push the dev branch.
	if out, err := c.runner(ctx, tmp, "git", "checkout", "-b", "dev"); err != nil {
		return fmt.Errorf("checkout -b dev: %w: %s", err, strings.TrimSpace(out))
	}
	if out, err := c.runner(ctx, tmp, "git", "push", "origin", "dev"); err != nil {
		return fmt.Errorf("push dev: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// lastHTTPS returns the last https:// token from a multiline string.
func lastHTTPS(s string) string {
	url := ""
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "https://") {
			url = tok
		}
	}
	return url
}
