package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"forge/internal/projects"
)

func ctxBG() context.Context { return context.Background() }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func projectWithOwner(id, owner string) projects.Project {
	return projects.Project{ID: id, OwnerID: owner}
}

func TestLinkCodeIssueConsume(t *testing.T) {
	l := newLinkCodeStore()
	now := time.Unix(1000, 0)
	code, err := l.issue("telegram", "usr-1", now)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong connector → no consume.
	if _, ok := l.consume("slack", code, now); ok {
		t.Fatal("code consumed under wrong connector")
	}
	// Correct consume returns the user and is single-use.
	uid, ok := l.consume("telegram", code, now)
	if !ok || uid != "usr-1" {
		t.Fatalf("consume = %q,%v, want usr-1,true", uid, ok)
	}
	if _, ok := l.consume("telegram", code, now); ok {
		t.Fatal("code must be single-use")
	}
}

func TestLinkCodeExpires(t *testing.T) {
	l := newLinkCodeStore()
	now := time.Unix(1000, 0)
	code, _ := l.issue("telegram", "usr-1", now)
	later := now.Add(linkCodeTTL + time.Second)
	if _, ok := l.consume("telegram", code, later); ok {
		t.Fatal("expired code must not consume")
	}
}

// The full link flow: issue a code, then a /link message binds the telegram user.
func TestTelegramLinkFlow(t *testing.T) {
	s, au, pr := membersServer(t)
	_ = pr
	owner, _ := au.CreateUser("owner@example.com", "password123")

	code, err := s.linkCodes.issue("telegram", owner.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reply := s.handleTelegramText(ctxBG(), "tg-555", "chat-1", "/link "+code)
	if !contains(reply, "Vinculado") {
		t.Fatalf("link reply = %q, want success", reply)
	}
	// Identity is bound now.
	uid, ok, _ := au.UserForChannelIdentity("telegram", "tg-555")
	if !ok || uid != owner.ID {
		t.Fatalf("identity = %q,%v, want owner,true", uid, ok)
	}
}

// A linked user messaging a chat bound to their project passes the access chain and
// reaches the Brain dispatch (here Brain is nil → "no disponible"), proving identity
// → project → role resolved.
func TestTelegramFreeTextReachesBrainDispatch(t *testing.T) {
	s, au, pr := membersServer(t)
	owner, _ := au.CreateUser("owner@example.com", "password123")
	if _, err := pr.Create(projectWithOwner("p1", owner.ID)); err != nil {
		t.Fatal(err)
	}
	_ = pr.LinkChannel("p1", "telegram", "chat-9", "*")
	_ = au.BindChannelIdentity("telegram", "tg-1", owner.ID)

	reply := s.handleTelegramText(ctxBG(), "tg-1", "chat-9", "hola")
	if !contains(reply, "asistente no está disponible") {
		t.Fatalf("reply = %q, want brain-unavailable (chain resolved)", reply)
	}
}

// /approve from a viewer is rejected (editor+ required); the chain still resolved.
func TestTelegramApproveRoleGate(t *testing.T) {
	s, au, pr := membersServer(t)
	owner, _ := au.CreateUser("owner@example.com", "password123")
	viewer, _ := au.CreateUser("viewer@example.com", "password123")
	if _, err := pr.Create(projectWithOwner("p1", owner.ID)); err != nil {
		t.Fatal(err)
	}
	_ = pr.LinkChannel("p1", "telegram", "chat-9", "*")
	_ = pr.AddMember("p1", viewer.ID, projects.RoleViewer)
	_ = au.BindChannelIdentity("telegram", "tg-v", viewer.ID)

	reply := s.handleTelegramText(ctxBG(), "tg-v", "chat-9", "/approve act-123")
	if !contains(reply, "editor") {
		t.Fatalf("reply = %q, want editor-required", reply)
	}
}

func TestParseResolveCmd(t *testing.T) {
	if id, ap, ok := parseResolveCmd("/approve act-1"); !ok || !ap || id != "act-1" {
		t.Fatalf("approve parse = %q,%v,%v", id, ap, ok)
	}
	if id, ap, ok := parseResolveCmd("/reject act-2"); !ok || ap || id != "act-2" {
		t.Fatalf("reject parse = %q,%v,%v", id, ap, ok)
	}
	if _, _, ok := parseResolveCmd("hola mundo"); ok {
		t.Fatal("plain text should not parse as resolve")
	}
}

func TestDataField(t *testing.T) {
	if got := dataField(`{"text":"hi","n":3}`, "text"); got != "hi" {
		t.Fatalf("dataField text = %q, want hi", got)
	}
	if got := dataField(`{"text":"hi"}`, "missing"); got != "" {
		t.Fatalf("dataField missing = %q, want empty", got)
	}
	if got := dataField("", "text"); got != "" {
		t.Fatalf("dataField empty data = %q, want empty", got)
	}
}

// An unlinked telegram user is told to link first.
func TestTelegramUnlinkedUser(t *testing.T) {
	s, _, _ := membersServer(t)
	reply := s.handleTelegramText(ctxBG(), "tg-unknown", "chat-9", "status")
	if !contains(reply, "vinculado") {
		t.Fatalf("reply = %q, want link instructions", reply)
	}
}
