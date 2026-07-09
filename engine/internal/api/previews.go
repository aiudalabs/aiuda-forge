package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forge/internal/httpx"
	"forge/internal/projects"
)

// previewTokenTTL bounds how long a minted preview token is usable — short, because
// it rides in a URL the untrusted preview can read. Long enough to open + navigate.
const previewTokenTTL = 10 * time.Minute

// servePreview serves a static preview published by the `release` step from
// PreviewsRoot/{project}/{run}/... The route sits behind the mandatory-auth
// middleware (a session or the service token is required; the browser passes the
// session as ?token= for a plain navigation, mirroring /ws), and the caller must be
// a member of {project}. There is NO directory listing: a directory resolves to its
// index.html or 404. Path traversal outside the run's dir is refused.
func (s *Server) servePreview(w http.ResponseWriter, r *http.Request) {
	if s.PreviewsRoot == "" {
		httpErr(w, http.StatusNotFound, "previews are not configured")
		return
	}
	// C2 extended to serving: a preview is UNTRUSTED repo content. Two headers make
	// that safe regardless of what the artifact's JS tries to do:
	//   · CSP `sandbox allow-scripts allow-forms` — the document runs in a UNIQUE
	//     OPAQUE origin (no allow-same-origin). Its scripts run, but every request it
	//     makes is cross-origin from Origin: null, so it cannot make a same-origin
	//     authenticated call back to this control-plane API (and CORS rejects null).
	//     This is the second lock behind preview-scoped tokens: even if the JS reads
	//     its own ?token=, that token is useless off /previews AND the call is blocked.
	//   · X-Content-Type-Options: nosniff — no MIME sniffing, so a file cannot be
	//     coerced into executing as a different, more dangerous type.
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	project := r.PathValue("project")
	run := r.PathValue("run")
	if !s.canAccessProject(r.Context(), project) {
		// 404, not 403 — no existence leak (same contract as the runs routes).
		httpErr(w, http.StatusNotFound, "preview not found")
		return
	}

	// Resolve the on-disk path and confirm it stays inside this run's directory
	// (defense in depth on top of the segment-sanitizing done when publishing).
	base := filepath.Join(s.PreviewsRoot, filepath.Base(project), filepath.Base(run))
	rel := filepath.Clean("/" + r.PathValue("path")) // leading slash neutralizes ".."
	target := filepath.Join(base, rel)
	if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		httpErr(w, http.StatusNotFound, "preview not found")
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		httpErr(w, http.StatusNotFound, "preview not found")
		return
	}
	// A directory serves its index.html (never a listing).
	if info.IsDir() {
		target = filepath.Join(target, "index.html")
		if fi, err := os.Stat(target); err != nil || fi.IsDir() {
			httpErr(w, http.StatusNotFound, "preview not found")
			return
		}
	}
	// http.ServeFile would honor its own redirect/index behavior and could list a
	// dir; we resolved the file ourselves, so serve the content directly with the
	// correct type inferred from the extension.
	f, err := os.Open(target)
	if err != nil {
		httpErr(w, http.StatusNotFound, "preview not found")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "preview read error")
		return
	}
	http.ServeContent(w, r, filepath.Base(target), fi.ModTime(), f)
}

// mintPreviewToken issues a short-lived, path-scoped preview token so the console can
// open a preview WITHOUT ever putting a session/service token in the URL. This route
// IS session-authenticated (the mandatory-auth middleware) and gated on project
// membership; the token it returns authorizes ONLY /previews/{project}/{run}/ for
// previewTokenTTL. POST /projects/{id}/previews/{run}/token.
func (s *Server) mintPreviewToken(w http.ResponseWriter, r *http.Request) {
	if len(s.PreviewSecret) == 0 || s.PreviewsRoot == "" {
		httpErr(w, http.StatusServiceUnavailable, "previews are not configured")
		return
	}
	project := r.PathValue("id")
	run := r.PathValue("run")
	// Only a member (viewer+) of the project may open its previews.
	if !s.requireRole(r.Context(), project, projects.RoleViewer) {
		httpErr(w, http.StatusNotFound, "preview not found") // 404, no existence leak
		return
	}
	// The preview must actually exist on disk (a token for a missing preview is useless
	// and misleading). filepath.Base defends the lookup against odd ids.
	dir := filepath.Join(s.PreviewsRoot, filepath.Base(project), filepath.Base(run))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		httpErr(w, http.StatusNotFound, "preview not found")
		return
	}
	tok := httpx.MintPreviewToken(s.PreviewSecret, project, run, previewTokenTTL, time.Now())
	base := strings.TrimRight(s.PreviewsBaseURL, "/")
	url := base + "/previews/" + project + "/" + run + "/?token=" + tok
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"url":        url,
		"expires_in": int(previewTokenTTL.Seconds()),
	})
}
