// Package imports brings external issues into the native backlog (v1.3). It maps a
// source's issues to tickets.Story rows, keyed by external_ref so re-importing the
// same issue is a no-op. Fetching (the gh CLI) lives in internal/github; this
// package owns the mapping + idempotent create, decoupled behind IssueLister.
package imports

import (
	"context"
	"fmt"
	"strings"

	"forge/internal/github"
	"forge/internal/tickets"
)

// IssueLister fetches a repo's issues. internal/github.Client implements it; tests
// inject a fake.
type IssueLister interface {
	ListIssues(ctx context.Context, repoURL string) ([]github.Issue, error)
}

// Result reports what an import did.
type Result struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"` // already present (by external_ref)
	StoryIDs []string `json:"story_ids"`
}

// GitHub imports the open issues of repoURL into projectID's backlog as stories.
// Each story carries external_ref "github:owner/repo#N"; an issue already imported
// (same external_ref) is skipped, making the import idempotent. Imported stories
// start in backlog with the repo set, ready to be enriched/scheduled.
func GitHub(ctx context.Context, store *tickets.Store, lister IssueLister, projectID, repoURL string) (Result, error) {
	slug, err := github.Slug(repoURL)
	if err != nil {
		return Result{}, fmt.Errorf("resolve repo slug: %w", err)
	}
	issues, err := lister.ListIssues(ctx, repoURL)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, iss := range issues {
		ref := fmt.Sprintf("github:%s#%d", slug, iss.Number)
		if _, exists, err := store.StoryIDByExternalRef(ref); err != nil {
			return res, err
		} else if exists {
			res.Skipped++
			continue
		}
		st := tickets.Story{
			ID:          "gh-" + strings.ReplaceAll(slug, "/", "-") + "-" + fmt.Sprint(iss.Number),
			Title:       iss.Title,
			Body:        iss.Body,
			Status:      tickets.StatusBacklog,
			Repo:        repoURL,
			ProjectID:   projectID,
			ExternalRef: ref,
		}
		if err := store.CreateStory(st); err != nil {
			// A concurrent import that created the same story between our check and
			// insert trips the external_ref/PK unique index — treat as skipped, not
			// a failure, to keep the import idempotent under races.
			if isAlreadyExists(err) {
				res.Skipped++
				continue
			}
			return res, fmt.Errorf("create story for %s: %w", ref, err)
		}
		res.Imported++
		res.StoryIDs = append(res.StoryIDs, st.ID)
	}
	return res, nil
}

// isAlreadyExists reports whether err is a sqlite UNIQUE violation (external_ref
// index or primary key) — the idempotency signal during a racing import.
func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
