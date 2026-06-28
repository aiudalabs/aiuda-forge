package settings

import (
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// An MCP secret (e.g. a Telegram bot token) is masked on read but readable raw via
// MCPValue, and survives a masked round-trip through Put.
func TestMCPSecretMaskAndRoundTrip(t *testing.T) {
	st := openTemp(t)
	in := Settings{MCP: map[string]map[string]any{
		"telegram": {"token": "REALBOTTOKEN", "chat_default": "123"},
	}}
	if _, err := st.Put(in); err != nil {
		t.Fatal(err)
	}
	// Raw read returns the real token.
	if got := st.MCPValue("telegram", "token"); got != "REALBOTTOKEN" {
		t.Fatalf("MCPValue token = %q, want REALBOTTOKEN", got)
	}
	// Get() masks the token but not the non-secret field.
	masked := st.Get()
	if masked.MCP["telegram"]["token"] != secretMask {
		t.Fatalf("token not masked on Get(): %v", masked.MCP["telegram"]["token"])
	}
	if masked.MCP["telegram"]["chat_default"] != "123" {
		t.Fatalf("non-secret field should be visible: %v", masked.MCP["telegram"]["chat_default"])
	}
	// A client round-trip (PUT the masked value back) must NOT wipe the real token.
	if _, err := st.Put(masked); err != nil {
		t.Fatal(err)
	}
	if got := st.MCPValue("telegram", "token"); got != "REALBOTTOKEN" {
		t.Fatalf("token wiped by masked round-trip: %q", got)
	}
}

// MCPValue matches the connector name case-insensitively (the console lets the user
// type "Telegram" or "telegram"; both must resolve the token).
func TestMCPValueCaseInsensitive(t *testing.T) {
	st := openTemp(t)
	_, _ = st.Put(Settings{MCP: map[string]map[string]any{"Telegram": {"token": "BOTX"}}})
	if got := st.MCPValue("telegram", "token"); got != "BOTX" {
		t.Fatalf("MCPValue(telegram) = %q, want BOTX (case-insensitive match on 'Telegram')", got)
	}
}

// Providing a new real token overwrites the stored one.
func TestMCPSecretOverwrite(t *testing.T) {
	st := openTemp(t)
	_, _ = st.Put(Settings{MCP: map[string]map[string]any{"telegram": {"token": "OLD"}}})
	_, _ = st.Put(Settings{MCP: map[string]map[string]any{"telegram": {"token": "NEW"}}})
	if got := st.MCPValue("telegram", "token"); got != "NEW" {
		t.Fatalf("token = %q, want NEW", got)
	}
}
