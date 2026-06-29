package api

import (
	"strings"
	"testing"
)

func TestGhDescriptionTruncatesAndCleans(t *testing.T) {
	long := strings.Repeat("a", 500)
	if got := ghDescription(long); len([]rune(got)) != 350 {
		t.Fatalf("len = %d, want 350", len([]rune(got)))
	}
	// control chars (newlines/tabs) → collapsed to single spaces, no control left
	got := ghDescription("Línea uno.\nLínea dos.\tTab\r\n\n  fin")
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("control chars remain: %q", got)
	}
	if got != "Línea uno. Línea dos. Tab fin" {
		t.Fatalf("got %q", got)
	}
	if ghDescription("short") != "short" {
		t.Fatal("short unchanged")
	}
}
