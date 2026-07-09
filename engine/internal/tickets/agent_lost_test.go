package tickets_test

import (
	"testing"

	"forge/internal/tickets"
)

// TestAgentLostMarkerLifecycle covers the conductor's "agente perdido" recovery
// note: MarkAgentLost stamps it, it survives a reload (persisted column), a fresh
// dispatch (SetStorySession) clears it, and it is cleared explicitly on ack.
func TestAgentLostMarkerLifecycle(t *testing.T) {
	st := openStore(t)
	if err := st.CreateStory(tickets.Story{ID: "S-01", ProjectID: "p1", ExternalRef: "github:o/r#1"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Fresh story: no note.
	if s, _ := st.GetStoryInProject("p1", "S-01"); s.AgentLost != "" {
		t.Fatalf("nueva story AgentLost=%q, want vacío", s.AgentLost)
	}

	// Mark lost — persisted and readable.
	if err := st.MarkAgentLost("p1", "S-01", "task purgada: abc123"); err != nil {
		t.Fatalf("MarkAgentLost: %v", err)
	}
	if s, _ := st.GetStoryInProject("p1", "S-01"); s.AgentLost != "task purgada: abc123" {
		t.Fatalf("AgentLost=%q tras marcar", s.AgentLost)
	}
	// It rides the project listing (the /tickets projection reads that path).
	list, _ := st.ListStoriesByProject("p1")
	if len(list) != 1 || list[0].AgentLost != "task purgada: abc123" {
		t.Fatalf("ListStoriesByProject perdió AgentLost: %+v", list)
	}

	// A fresh dispatch (SetStorySession) clears the note — a re-dispatched story is
	// no longer lost.
	if err := st.SetStorySession("S-01", "https://github.com/o/r/tasks/new-1"); err != nil {
		t.Fatalf("SetStorySession: %v", err)
	}
	if s, _ := st.GetStoryInProject("p1", "S-01"); s.AgentLost != "" {
		t.Fatalf("AgentLost=%q tras re-despacho, want vacío", s.AgentLost)
	}

	// ClearStorySession drops the session pointer (dead-task cleanup path).
	if err := st.ClearStorySession("S-01"); err != nil {
		t.Fatalf("ClearStorySession: %v", err)
	}
	if urls, _ := st.SessionURLs("p1"); urls["S-01"] != "" {
		t.Fatalf("sesión de S-01 debía quedar limpia, got %q", urls["S-01"])
	}

	// Explicit clear (operator ack).
	if err := st.MarkAgentLost("p1", "S-01", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkAgentLost("p1", "S-01", ""); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStoryInProject("p1", "S-01"); s.AgentLost != "" {
		t.Fatalf("AgentLost=%q tras clear explícito", s.AgentLost)
	}
}

// TestMarkAgentLostScoped: with a project scope, a same-id story in another
// project is untouched (the S11-01 cross-tenant contract).
func TestMarkAgentLostScoped(t *testing.T) {
	st := openStore(t)
	for _, p := range []string{"pa", "pb"} {
		if err := st.CreateStory(tickets.Story{ID: "S-01", ProjectID: p, ExternalRef: "github:o/" + p + "#1"}); err != nil {
			t.Fatalf("create %s: %v", p, err)
		}
	}
	if err := st.MarkAgentLost("pa", "S-01", "lost"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.GetStoryInProject("pa", "S-01"); s.AgentLost != "lost" {
		t.Fatalf("pa/S-01 AgentLost=%q, want lost", s.AgentLost)
	}
	if s, _ := st.GetStoryInProject("pb", "S-01"); s.AgentLost != "" {
		t.Fatalf("pb/S-01 AgentLost=%q, want vacío (no cross-tenant)", s.AgentLost)
	}
}
