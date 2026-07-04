package billing

import "testing"

func TestDefaultPolicyRoutesByTaskType(t *testing.T) {
	cases := []struct {
		name            string
		stepType, agent string
		quality         string
		wantModel       string
	}{
		{"decomposition → cheap", "design", "scrum-master", "", modelCheap},
		{"story detail → cheap", "agent", "story-detailer", "", modelCheap},
		{"analyst → cheap", "design", "analyst", "", modelCheap},
		{"architect → capable", "design", "architect", "", modelCapable},
		{"quality:high → capable", "agent", "dev", "high", modelCapable},
		{"plain dev → no opinion (manifest default)", "agent", "dev", "", ""},
		{"reviewer → no opinion", "agentic_verify", "reviewer", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, model := DefaultPolicy.Resolve(c.stepType, c.agent, c.quality)
			if model != c.wantModel {
				t.Fatalf("Resolve(%q,%q,%q) model = %q, want %q", c.stepType, c.agent, c.quality, model, c.wantModel)
			}
		})
	}
}

// First matching rule wins, and an empty Engine/Model means "no opinion".
func TestRoutingFirstMatchWins(t *testing.T) {
	p := RoutingPolicy{Rules: []RoutingRule{
		{Agent: "dev", Quality: "high", Model: "capable-x"},
		{Agent: "dev", Model: "cheap-x"},
	}}
	if _, m := p.Resolve("agent", "dev", "high"); m != "capable-x" {
		t.Fatalf("quality:high dev should match the first rule, got %q", m)
	}
	if _, m := p.Resolve("agent", "dev", ""); m != "cheap-x" {
		t.Fatalf("plain dev should fall to the second rule, got %q", m)
	}
	if _, m := p.Resolve("agent", "other", ""); m != "" {
		t.Fatalf("no rule should yield no opinion, got %q", m)
	}
}
