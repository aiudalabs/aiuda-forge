// Package billing is the SaaS metering + entitlements engine. We absorb the LLM
// token cost (our provider keys), and bill per COMPLETED FEATURE — a dev task that
// passed the gate AND whose PR was MERGED (status `done`). FAILED/CANCELLED/unmerged
// work never bills. The engine protects our margin: it measures what we actually
// spend (real token cost, incl. failed work), caps what a customer can consume per
// their plan, and enforces a hard per-workspace spend cap as our own backstop.
//
// This file is the central plan catalog — changing a plan is editing this object,
// never an endpoint. Stripe / pricing UI / real charging live OUTSIDE this package.
package billing

// Plan is a billing tier. Prices are USD. IncludedFeatures is per billing cycle,
// except FREE whose features are lifetime (LifetimeIncluded). OveragePriceUSD == 0
// means no overage (FREE: hitting the cap requires an upgrade). DefaultSpendCapUSD
// is OUR hard backstop on real token cost per cycle (independent of the customer's
// entitlement) — a runaway/abuse protection, tunable per plan.
type Plan struct {
	ID                 string
	BasePriceUSD       float64
	IncludedFeatures   int
	LifetimeIncluded   bool
	OveragePriceUSD    float64
	MemberLimit        int
	ProjectLimit       int // 0 = unlimited
	DefaultSpendCapUSD float64
}

// Plans is the catalog. Edit here to change tiers; nothing else hardcodes them.
var Plans = map[string]Plan{
	"free": {
		ID: "free", BasePriceUSD: 0,
		IncludedFeatures: 2, LifetimeIncluded: true, OveragePriceUSD: 0,
		MemberLimit: 1, ProjectLimit: 1, DefaultSpendCapUSD: 10,
	},
	"indie": {
		ID: "indie", BasePriceUSD: 39,
		IncludedFeatures: 15, OveragePriceUSD: 3.50,
		MemberLimit: 1, ProjectLimit: 0, DefaultSpendCapUSD: 150,
	},
	"team": {
		ID: "team", BasePriceUSD: 149,
		IncludedFeatures: 60, OveragePriceUSD: 3.00,
		MemberLimit: 5, ProjectLimit: 0, DefaultSpendCapUSD: 600,
	},
}

// DefaultPlanID is the plan a new workspace starts on.
const DefaultPlanID = "free"

// PlanFor returns the plan by id, falling back to the default plan for an unknown
// id so a corrupt/missing plan_id never panics the entitlement path.
func PlanFor(id string) Plan {
	if p, ok := Plans[id]; ok {
		return p
	}
	return Plans[DefaultPlanID]
}
