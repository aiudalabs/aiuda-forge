package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	github "forge/internal/github"
	"forge/internal/httpx"
	"forge/internal/projects"
)

// ---- helpers ----------------------------------------------------------------

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// ghDescription sanitizes a project description for GitHub's repo description, which
// rejects BOTH control characters (newlines/tabs → HTTP 422) and anything over 350
// chars. It replaces control runes with spaces, collapses whitespace, then truncates
// by rune (never splitting a multibyte boundary). The project row keeps the full text.
func ghDescription(d string) string {
	const max = 350
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f { // C0 control chars (incl. \n \r \t) and DEL
			return ' '
		}
		return r
	}, d)
	clean = strings.Join(strings.Fields(clean), " ") // collapse runs of whitespace
	r := []rune(clean)
	if len(r) <= max {
		return clean
	}
	return string(r[:max-1]) + "…"
}

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
	// Repo is optional: when supplied, the project adopts that existing GitHub
	// repo (validated) instead of creating a new one.
	Repo string `json:"repo"`
	// Org is the owner (user or org) to create the repo under; defaults to the
	// server's GH org. RepoName overrides the repo slug (defaults to slug(Name)).
	Org      string `json:"org"`
	RepoName string `json:"repo_name"`
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
	if req.RepoName != "" {
		slug = toSlug(req.RepoName)
	}

	// Adopt the supplied repo (validated at the boundary, audit C5) or create a
	// fresh GitHub repo under the org.
	repoURL := req.Repo
	if repoURL != "" {
		if err := httpx.ValidateRemote(repoURL); err != nil {
			httpErr(w, http.StatusBadRequest, "invalid repo: "+err.Error())
			return
		}
	} else {
		org := s.ghOrg()
		if req.Org != "" {
			org = req.Org
		}
		gh := s.ghForUser(r.Context())
		// GitHub caps a repo description at 350 chars (a longer one fails the create
		// with HTTP 422). Truncate for the repo; the full description is stored on the
		// project row below.
		created, err := gh.CreateRepo(r.Context(), org, slug, ghDescription(req.Description), true)
		if err != nil {
			if errors.Is(err, github.ErrRepoExists) {
				httpErr(w, http.StatusConflict, "github repo already exists: "+org+"/"+slug)
				return
			}
			httpErr(w, http.StatusInternalServerError, "create github repo: "+err.Error())
			return
		}
		repoURL = created
		if err := gh.EnsureDevBranch(r.Context(), repoURL); err != nil {
			// Non-fatal: repo is created; log so it's visible and can be diagnosed.
			log.Printf("EnsureDevBranch %s: %v", repoURL, err)
		}
	}

	// owner_id is the authenticated user the auth middleware resolved (audit A1).
	// Empty in open/dev mode or for a service-token caller — the project is then
	// unowned, which is acceptable for single-user/local use.
	ownerID := httpx.UserIDFromContext(r.Context())

	id := slug + "-" + shortID()
	p := projects.Project{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Repo:        repoURL,
		OwnerID:     ownerID,
		// ExecutionUnit/MergeMode default to sprint/manual in the store.
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
	// Owner-scoping (audit A1): a logged-in user sees ONLY their own projects. A
	// service-token caller (the orchestrator) or open/dev mode resolves to no user
	// and sees all projects — they are trusted/admin contexts, not a browser user.
	ownerID := httpx.UserIDFromContext(r.Context())
	var (
		list []projects.Project
		err  error
	)
	if ownerID != "" {
		// v1.2 roles: a user sees every project they are a member of (owner·editor
		// ·viewer), not only the ones they own — so invited collaborators see shared
		// projects. A service-token/open-mode caller (no user) still sees all.
		list, err = s.Projects.ListForMember(ownerID)
	} else {
		list, err = s.Projects.List()
	}
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []projects.Project{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": list})
}

// ---- GET /projects/{id}/settings, PUT /projects/{id}/settings ---------------

func (s *Server) getProjectSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	set, err := s.Projects.GetSettings(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) putProjectSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Changing execution settings is a write — viewers are read-only (v1.2 gating).
	if !s.requireRole(r.Context(), id, projects.RoleEditor) {
		httpErr(w, http.StatusForbidden, "changing settings requires editor or owner")
		return
	}
	var in projects.Settings
	if !readJSON(w, r, &in) {
		return
	}
	out, err := s.Projects.PutSettings(id, in)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		if errors.Is(err, projects.ErrInvalid) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- GET /projects/{id} -----------------------------------------------------

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
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

// docRef returns the git ref to read docs from (?ref=), defaulting to "dev" —
// the factory's integration branch, where the design handoff commits the specs.
func docRef(r *http.Request) string {
	if ref := r.URL.Query().Get("ref"); ref != "" {
		return ref
	}
	return "design" // the per-project design branch = the approved design history
}

// getProjectDocHistory lists the version history (commits) of one design doc on the
// ref (default `design`) — powers the per-doc version chips/timeline.
func (s *Server) getProjectDocHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	ref := docRef(r)
	path := r.URL.Query().Get("path")
	if !strings.HasPrefix(path, "docs/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?&") {
		httpErr(w, http.StatusBadRequest, "path must be under docs/")
		return
	}
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.Repo == "" {
		writeJSON(w, http.StatusOK, map[string]any{"path": path, "ref": ref, "versions": []github.FileCommit{}})
		return
	}
	commits, err := s.ghFor(r.Context(), id).ListCommitsForPath(r.Context(), p.Repo, ref, path)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "ref": ref, "versions": commits})
}

// getProjectDesignLog lists the recent commits on the design branch — the project's
// design changelog (each = a doc version published on a gate approval).
func (s *Server) getProjectDesignLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	ref := docRef(r)
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.Repo == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ref": ref, "commits": []github.FileCommit{}})
		return
	}
	commits, err := s.ghFor(r.Context(), id).ListCommits(r.Context(), p.Repo, ref)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ref": ref, "commits": commits})
}

// listProjectDocs lists the project's repo docs/ tree (U1: Studio = Confluence).
// Reads from the repo via gh so the specs survive an ephemeral/purged design run.
// A repo with no docs yet returns an empty list (not an error) so the UI shows an
// "in progress" empty state rather than failing.
func (s *Server) listProjectDocs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	ref := docRef(r)
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p.Repo == "" {
		writeJSON(w, http.StatusOK, map[string]any{"docs": []github.DocEntry{}, "ref": ref})
		return
	}
	gh := s.ghFor(r.Context(), id)
	entries, err := gh.ListContents(r.Context(), p.Repo, "docs", ref)
	if err != nil {
		// No docs/ on this ref yet (design not handed off, or wrong branch).
		writeJSON(w, http.StatusOK, map[string]any{"docs": []github.DocEntry{}, "ref": ref, "note": err.Error()})
		return
	}
	// Expand subdirectories one level so the UI sees individual files
	// (e.g. docs/mockups/passenger-app.html) instead of a dir entry that
	// forces the frontend to guess filenames. The dir entry itself is dropped;
	// its children replace it. A subdirectory with no readable contents is
	// silently skipped (best effort — the list is already available).
	var flat []github.DocEntry
	for _, e := range entries {
		if e.Type != "dir" {
			flat = append(flat, e)
			continue
		}
		sub, serr := gh.ListContents(r.Context(), p.Repo, e.Path, ref)
		if serr != nil {
			continue // subdir unreadable — skip
		}
		for _, s := range sub {
			if s.Type == "file" {
				flat = append(flat, s)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": flat, "ref": ref})
}

// getProjectDoc returns the decoded content of one doc file (?path=docs/PRD.md).
// The path is constrained to docs/ to avoid reading arbitrary repo files.
func (s *Server) getProjectDoc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canAccessProject(r.Context(), id) {
		httpErr(w, http.StatusNotFound, "project not found: "+id)
		return
	}
	ref := docRef(r)
	path := r.URL.Query().Get("path")
	if !strings.HasPrefix(path, "docs/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?&") {
		httpErr(w, http.StatusBadRequest, "path must be under docs/")
		return
	}
	p, err := s.Projects.Get(id)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			httpErr(w, http.StatusNotFound, "project not found: "+id)
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	content, err := s.ghFor(r.Context(), id).ReadFile(r.Context(), p.Repo, path, ref)
	if err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "content": content, "ref": ref})
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


// githubOrgs lists the owners (the gh user + their orgs) a repo can be created under —
// powers the org picker on project creation. Best-effort: a gh error still returns the
// configured default org so the picker is never empty.
func (s *Server) githubOrgs(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	var owners []string
	add := func(o string) {
		if o != "" && !seen[o] {
			seen[o] = true
			owners = append(owners, o)
		}
	}
	// The CURRENT user's owners (their login + orgs) via their token, or the host's if
	// they haven't connected GitHub — Owners() resolves that. Plus the configured default
	// so the picker is never empty.
	for _, o := range s.ghForUser(r.Context()).Owners(r.Context()) {
		add(o)
	}
	add(s.ghOrg())
	writeJSON(w, http.StatusOK, map[string]any{"owners": owners})
}
