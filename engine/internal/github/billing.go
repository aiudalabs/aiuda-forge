package github

// Enhanced-billing usage (F4 "Spend desde GitHub"): read the org's metered
// consumption (Actions minutes, Copilot premium requests, LFS, …) so the console
// can surface what the autonomous factory is costing. Same runner seam as the
// rest of the package; scoping is per-organization (GitHub's enhanced billing
// platform exposes usage at the org, not the repo, level).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrBillingUnavailable is returned when GitHub does not expose billing usage for
// the org that owns the repo — a personal account (no org billing endpoint), or a
// token without organization billing visibility. It is a benign "not available",
// NOT a system error: callers degrade to a placeholder rather than failing.
var ErrBillingUnavailable = errors.New("github billing usage not available")

// UsageItem is one row of the enhanced-billing usage report. Amounts are in USD.
// Fields mirror the `usageItems` shape returned by
// GET /organizations/{org}/settings/billing/usage.
type UsageItem struct {
	Date             string  `json:"date"`
	Product          string  `json:"product"` // "actions" | "copilot" | "git_lfs" | …
	SKU              string  `json:"sku"`
	Quantity         float64 `json:"quantity"`
	UnitType         string  `json:"unitType"` // "Minutes" | "GigabyteHours" | …
	PricePerUnit     float64 `json:"pricePerUnit"`
	GrossAmount      float64 `json:"grossAmount"`
	DiscountAmount   float64 `json:"discountAmount"`
	NetAmount        float64 `json:"netAmount"` // what is actually owed (gross − discount)
	OrganizationName string  `json:"organizationName"`
	RepositoryName   string  `json:"repositoryName"`
}

// BillingUsage returns the usage items for the org that owns repoURL, scoped to
// the given calendar year/month (a month is GitHub's billing cycle). It shells
// out to `gh api /organizations/<org>/settings/billing/usage?year=&month=`.
//
// A 403/404 (personal account, or a token without billing visibility) is mapped
// to ErrBillingUnavailable so the caller can degrade gracefully instead of
// erroring the request.
func (c *Client) BillingUsage(ctx context.Context, repoURL string, year, month int) ([]UsageItem, error) {
	slug, err := slugFromURL(repoURL)
	if err != nil {
		return nil, err
	}
	org := slug[:strings.IndexByte(slug, '/')] // owner of "owner/repo"
	endpoint := fmt.Sprintf("/organizations/%s/settings/billing/usage?year=%d&month=%d", org, year, month)
	out, err := c.runner(ctx, "", "gh", "api", endpoint)
	if err != nil {
		lo := strings.ToLower(out)
		if strings.Contains(out, "404") || strings.Contains(out, "403") ||
			strings.Contains(lo, "not found") || strings.Contains(lo, "scope") ||
			strings.Contains(lo, "must have admin") || strings.Contains(lo, "forbidden") {
			return nil, fmt.Errorf("%w (%s): %s", ErrBillingUnavailable, org, strings.TrimSpace(out))
		}
		return nil, fmt.Errorf("gh api %s: %w: %s", endpoint, err, strings.TrimSpace(out))
	}
	var resp struct {
		UsageItems []UsageItem `json:"usageItems"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("decode billing usage (%s): %w", org, err)
	}
	return resp.UsageItems, nil
}
