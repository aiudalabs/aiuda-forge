package github

// Docs reading: the Studio "Confluence" view renders a project's specs straight
// from its repo (the source of truth that survives an ephemeral design run). These
// helpers read the repo's docs/ tree via the GitHub contents API through `gh`.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// DocEntry is a file or directory under a repo path — the subset of the GitHub
// contents API the docs viewer needs.
type DocEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // "file" | "dir"
	Size int    `json:"size"`
}

// ListContents lists directory `dir` of repoURL at `ref` (e.g. "dev") via the
// GitHub contents API. A missing dir/ref yields an error the caller can treat as
// "no docs yet".
func (c *Client) ListContents(ctx context.Context, repoURL, dir, ref string) ([]DocEntry, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	api := fmt.Sprintf("repos/%s/contents/%s", slug, dir)
	if ref != "" {
		api += "?ref=" + ref
	}
	out, err := c.runner(ctx, "", "gh", "api", api)
	if err != nil {
		return nil, fmt.Errorf("gh api contents %s: %w: %s", dir, err, strings.TrimSpace(out))
	}
	var entries []DocEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("decode contents %s: %w", dir, err)
	}
	return entries, nil
}

// ReadFile returns the decoded UTF-8 content of repo file `path` at `ref`.
func (c *Client) ReadFile(ctx context.Context, repoURL, path, ref string) (string, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return "", err
	}
	api := fmt.Sprintf("repos/%s/contents/%s", slug, path)
	if ref != "" {
		api += "?ref=" + ref
	}
	// --jq .content yields the base64 blob (newline-wrapped per the contents API).
	out, err := c.runner(ctx, "", "gh", "api", api, "--jq", ".content")
	if err != nil {
		return "", fmt.Errorf("gh api file %s: %w: %s", path, err, strings.TrimSpace(out))
	}
	clean := strings.ReplaceAll(strings.TrimSpace(out), "\n", "")
	dec, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return "", fmt.Errorf("decode file %s: %w", path, err)
	}
	return string(dec), nil
}
