package api
import "testing"
func TestGhDescriptionTruncates(t *testing.T) {
	long := make([]rune, 500)
	for i := range long { long[i] = 'a' }
	got := ghDescription(string(long))
	if len([]rune(got)) != 350 { t.Fatalf("len = %d, want 350", len([]rune(got))) }
	if ghDescription("short") != "short" { t.Fatal("short unchanged") }
}
