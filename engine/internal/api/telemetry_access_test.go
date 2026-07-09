package api

// Auth + tenant-scoping coverage for GET /sprints/{id}/telemetry. The telemetry carries
// operator data (gate feedback text, spend), so it must sit behind the same httpx.Auth
// middleware as the rest of the control plane (401 without a credential) AND be scoped
// per project (a user session may only read a sprint of a project it belongs to; the
// service token is unrestricted).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"forge/internal/httpx"
	"forge/internal/store"
	"forge/internal/tickets"
)

// TestSprintTelemetry401WithoutCredential: the route is NOT in httpx.Auth's public list,
// so a request with no bearer (or a wrong one) is 401 before it ever reaches the handler;
// a valid service token routes through. This is the same middleware TestAuthServiceToken /
// TestAuthSessionToken cover generically — asserted here for the telemetry route itself.
func TestSprintTelemetry401WithoutCredential(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	tix, err := tickets.Open(filepath.Join(t.TempDir(), "tickets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(); tix.Close() })
	s := &Server{Store: st, Tickets: tix, linkCodes: newLinkCodeStore(), mux: http.NewServeMux()}
	s.routes()
	handler := httpx.Auth(httpx.AuthConfig{ServiceToken: "svc", Sessions: fakeSessions{"sess": "usr-1"}}, s)

	get := func(bearer string) int {
		req := httptest.NewRequest("GET", "/sprints/SP1/telemetry", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := get(""); c != http.StatusUnauthorized {
		t.Fatalf("no credential: want 401, got %d", c)
	}
	if c := get("wrong-token"); c != http.StatusUnauthorized {
		t.Fatalf("wrong credential: want 401, got %d", c)
	}
	if c := get("svc"); c == http.StatusUnauthorized {
		t.Fatalf("valid service token must pass the auth gate, got 401")
	}
}

// TestSprintTelemetryCrossTenantScoping: a user session may read telemetry ONLY for a
// sprint of a project it belongs to. Crucially, an EMPTY project_id from a user session
// is denied (no cross-project aggregation) — the leak this test guards against. The
// service token is unrestricted.
func TestSprintTelemetryCrossTenantScoping(t *testing.T) {
	s, aCtx, svcCtx := ticketsAccessServer(t) // user A owns pa; pb has sprint spb (user B)
	st, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s.Store = st // the aggregator needs a control store for the allowed paths

	call := func(ctx context.Context, sprintID, projectID string) (int, string) {
		url := "/sprints/" + sprintID + "/telemetry"
		if projectID != "" {
			url += "?project_id=" + projectID
		}
		req := httptest.NewRequest("GET", url, nil).WithContext(ctx)
		req.SetPathValue("id", sprintID)
		rec := httptest.NewRecorder()
		s.sprintTelemetry(rec, req)
		return rec.Code, rec.Body.String()
	}
	leaked := func(body string) bool { return strings.Contains(body, `"sprint_id"`) }

	// (a) user A, another tenant's project → denied, no telemetry body.
	if code, body := call(aCtx, "spb", "pb"); leaked(body) {
		t.Fatalf("user A read pb's telemetry (code=%d body=%s) — cross-tenant leak", code, body)
	}
	// (b) user A, NO project_id → denied (the gap): must NOT aggregate across tenants.
	if code, body := call(aCtx, "spb", ""); leaked(body) {
		t.Fatalf("user A with no project_id got telemetry (code=%d body=%s) — cross-tenant leak", code, body)
	}
	// (c) user A, their OWN project → allowed (real telemetry document).
	if _, body := call(aCtx, "spa", "pa"); !leaked(body) {
		t.Fatalf("user A must be able to read its own project's telemetry, got %s", body)
	}
	// (d) service token (uid-less context, e.g. the scheduler) → unrestricted.
	if _, body := call(svcCtx, "spb", ""); !leaked(body) {
		t.Fatalf("service token must read telemetry unrestricted, got %s", body)
	}
}
