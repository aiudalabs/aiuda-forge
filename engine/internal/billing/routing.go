package billing

// Model routing is the margin lever (step 5): assign a cheaper provider/model to
// cheap work (backlog decomposition, trivial planning) and a capable one to complex
// / quality-critical work. Lowering meter (a) — what we spend — WITHOUT changing the
// price the customer pays per feature. It is CONFIGURABLE DATA (this object), not
// fixed logic; the runner consults it ABOVE the agent manifest's model and BELOW an
// explicit per-step model override in the workflow.

// RoutingRule maps a task shape to an engine+model. First matching rule wins; an
// empty match field is a wildcard, an empty Engine/Model means "no opinion" (fall
// through to the manifest/default).
type RoutingRule struct {
	StepType string // workflow step type: "design" | "agent" | "agentic_verify" | "" (any)
	Agent    string // agent id: "scrum-master" | "dev" | "" (any)
	Quality  string // quality tag: "high" | "" (any)
	Engine   string // backend id (e.g. "claude" | "opencode"); "" = unchanged
	Model    string // model id; "" = unchanged
}

// RoutingPolicy is an ordered rule list.
type RoutingPolicy struct {
	Rules []RoutingRule
}

// Resolve returns the engine+model for a task by the first matching rule. Empty
// results mean the policy has no opinion — the caller keeps the manifest/default.
func (p RoutingPolicy) Resolve(stepType, agent, quality string) (engine, model string) {
	for _, r := range p.Rules {
		if (r.StepType == "" || r.StepType == stepType) &&
			(r.Agent == "" || r.Agent == agent) &&
			(r.Quality == "" || r.Quality == quality) {
			return r.Engine, r.Model
		}
	}
	return "", ""
}

// Model ids the policy routes between (cheapest → most capable).
const (
	modelCheap   = "claude-haiku-4-5-20251001"
	modelMid     = "claude-sonnet-4-6"
	modelCapable = "claude-opus-4-8"
)

// DefaultPolicy is the shipped margin lever. Edit here to retune cost — never the
// runner. Quality-critical work and the architect get the capable model; planning /
// decomposition agents get the cheap one; everything else falls through to the
// agent manifest's declared model.
var DefaultPolicy = RoutingPolicy{Rules: []RoutingRule{
	{Quality: "high", Model: modelCapable},
	{Agent: "architect", Model: modelCapable},
	{Agent: "scrum-master", Model: modelCheap},
	{Agent: "story-detailer", Model: modelCheap},
	{Agent: "analyst", Model: modelCheap},
}}
