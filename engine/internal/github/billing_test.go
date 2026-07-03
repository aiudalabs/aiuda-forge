package github

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestBillingUsageParses checks the org endpoint is called with the derived org
// and year/month, and that usageItems decode.
func TestBillingUsageParses(t *testing.T) {
	var calls []call
	reply := `{"usageItems":[
		{"date":"2026-07-01T00:00:00Z","product":"actions","sku":"Actions Linux","quantity":261.0,"unitType":"Minutes","pricePerUnit":0.006,"grossAmount":1.566,"discountAmount":1.566,"netAmount":0.0,"organizationName":"acme","repositoryName":"widgets"},
		{"date":"2026-07-02T00:00:00Z","product":"copilot","sku":"Copilot premium request","quantity":10.0,"unitType":"Requests","pricePerUnit":0.04,"grossAmount":0.4,"discountAmount":0.0,"netAmount":0.4,"organizationName":"acme","repositoryName":"widgets"}
	]}`
	c := withRunner(fakeRunner(map[string]string{"gh api": reply}, &calls))

	items, err := c.BillingUsage(context.Background(), "https://github.com/acme/widgets", 2026, 7)
	if err != nil {
		t.Fatalf("BillingUsage: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[1].Product != "copilot" || items[1].NetAmount != 0.4 {
		t.Errorf("second item = %+v", items[1])
	}
	// Endpoint must carry the derived org and the year/month filter.
	joined := strings.Join(calls[0].args, " ")
	for _, want := range []string{"/organizations/acme/settings/billing/usage", "year=2026", "month=7"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

// TestBillingUsageUnavailable maps a 404/scope error to ErrBillingUnavailable.
func TestBillingUsageUnavailable(t *testing.T) {
	c := withRunner(func(ctx context.Context, workdir, name string, args ...string) (string, error) {
		return `{"message":"Not Found","status":"404"}`, errExitCode1{}
	})
	_, err := c.BillingUsage(context.Background(), "https://github.com/personal/repo", 2026, 7)
	if !errors.Is(err, ErrBillingUnavailable) {
		t.Fatalf("want ErrBillingUnavailable, got %v", err)
	}
}
