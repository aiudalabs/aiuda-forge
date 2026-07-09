package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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
