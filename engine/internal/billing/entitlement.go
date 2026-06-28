package billing

import "fmt"

// Mode is the billing mode a feature would run under.
type Mode string

const (
	ModeIncluded Mode = "included"
	ModeOverage  Mode = "overage"
	ModeDenied   Mode = "denied"
)

// Decision is the entitlement verdict for starting one more billable feature.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Mode    Mode   `json:"mode"`
	Reason  string `json:"reason"`
}

// Entitlement decides whether a workspace may start ONE more billable feature, and
// at what mode. It is PREDICTIVE (consulted at fire-time, before burning tokens);
// the real charge happens at merge (CountFeature). Evaluation order:
//
//	spend-cap pause (ours) → FREE lifetime cap → paid included → paid overage
//	(within the customer's overage cap) → denied.
func (s *Store) Entitlement(workspaceID string) (Decision, error) {
	w, err := s.GetWorkspace(workspaceID)
	if err != nil {
		return Decision{}, err
	}
	if w.Paused {
		reason := w.PausedReason
		if reason == "" {
			reason = "spend cap reached"
		}
		return Decision{Mode: ModeDenied, Reason: "workspace paused: " + reason}, nil
	}
	plan := PlanFor(w.PlanID)
	c, err := s.ActiveCycle(workspaceID)
	if err != nil {
		return Decision{}, err
	}

	if plan.LifetimeIncluded { // FREE: lifetime allowance, no overage
		if w.LifetimeFeatures < plan.IncludedFeatures {
			return Decision{Allowed: true, Mode: ModeIncluded,
				Reason: fmt.Sprintf("free tier (%d/%d courtesy features)", w.LifetimeFeatures, plan.IncludedFeatures)}, nil
		}
		return Decision{Mode: ModeDenied, Reason: "free tier exhausted — upgrade to continue"}, nil
	}

	if c.IncludedConsumed < plan.IncludedFeatures {
		return Decision{Allowed: true, Mode: ModeIncluded,
			Reason: fmt.Sprintf("within included (%d/%d this cycle)", c.IncludedConsumed, plan.IncludedFeatures)}, nil
	}
	if plan.OveragePriceUSD > 0 {
		// Customer overage cap: stop before the NEXT overage feature would exceed it.
		if w.OverageCapUSD > 0 && (float64(c.OverageConsumed)+1)*plan.OveragePriceUSD > w.OverageCapUSD {
			return Decision{Mode: ModeDenied, Reason: "overage cap reached"}, nil
		}
		return Decision{Allowed: true, Mode: ModeOverage, Reason: "overage"}, nil
	}
	return Decision{Mode: ModeDenied, Reason: "included exhausted, no overage on this plan"}, nil
}

// SetOverageCap sets the customer's overage spend cap (USD). 0 = unlimited.
func (s *Store) SetOverageCap(workspaceID string, capUSD float64) error {
	_, err := s.db.Exec(`UPDATE workspaces SET overage_cap_usd=? WHERE id=?`, capUSD, workspaceID)
	return err
}

// SetPaused pauses/unpauses a workspace (the spend-cap guard, step 4, pauses it).
func (s *Store) SetPaused(workspaceID string, paused bool, reason string) error {
	_, err := s.db.Exec(`UPDATE workspaces SET paused=?, paused_reason=? WHERE id=?`, b2i(paused), reason, workspaceID)
	return err
}
