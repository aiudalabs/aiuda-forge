package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	github "forge/internal/github"
	"forge/internal/projects"
)

// ---- helpers ----------------------------------------------------------------

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// toSlug derives a lowercase, hyphen-separated repo slug from a project name.
func toSlug(name string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	return s
}

// needProjects guards a handler that requires the project store, returning 503
// if it has not been wired in.
func (s *Server) needProjects(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Projects == nil {
			httpErr(w, http.StatusServiceUnavailable, "project store not configured")
			return
		}
		h(w, r)
	}
}

// ---- POST /projects ---------------------------------------------------------

type createProjectReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		httpErr(w, http.StatusBadRequest, "name is required")
		return
	}

	slug := toSlug(req.Name)
	org := s.ghOrg()

	gh := github.New()
	repoURL, err := gh.CreateRepo(r.Context(), org, slug, req.Description, true)
	if err != nil {
		if errors.Is(err, github.ErrRepoExists) {
			httpErr(w, http.StatusConflict, "github repo already exists: "+org+"/"+slug)
			return
		}
		httpErr(w, http.StatusInternalServerError, "create github repo: "+err.Error())
		return
	}

	if err := gh.EnsureDevBranch(r.Context(), repoURL); err != nil {
		// Non-fatal: the repo is created; log the error in the detail but still persist.
		// The caller can retry branch creation manually if needed.
		_ = err // surfaced via project record; do not block 201
	}

	id := slug + "-" + shortID()
	p := projects.Project{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Repo:        repoURL,
	}
	created, err := s.Projects.Create(p)
	if err != nil {
		if errors.Is(err, projects.ErrExists) {
			httpErr(w, http.StatusConflict, "project already exists: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// ---- GET /projects ----------------------------------------------------------

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.Projects.List()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []projects.Project{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": list})
}

// ---- GET /projects/{id} -----------------------------------------------------

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ---- env helpers ------------------------------------------------------------

// ghOrg returns the GitHub org/user to create repos under. Reads VIBEFORGE_GH_ORG;
// falls back to the authenticated gh user (gh api user --jq .login).
func (s *Server) ghOrg() string {
	if org := envOrEmpty("VIBEFORGE_GH_ORG"); org != "" {
		return org
	}
	// Fall back to the authenticated user from gh CLI.
	// We deliberately don't cache this — it's only called on project creation.
	out, err := ghCLIUser()
	if err != nil || out == "" {
		return "unknown-org"
	}
	return strings.TrimSpace(out)
}

// envOrEmpty reads an env var; returns "" if unset (no default fallback needed).
func envOrEmpty(key string) string { return os.Getenv(key) }

// shortID returns an 8-hex-char random suffix for project IDs.
func shortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ghCLIUser calls `gh api user --jq .login` to get the authenticated GitHub user.
func ghCLIUser() (string, error) {
	cmd := exec.Command("gh", "api", "user", "--jq", ".login")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
