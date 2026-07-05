package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"forge/internal/agent"
	"forge/internal/workflow"
)

// regIDRe is the same anti-traversal id guard the kernel loaders use: an id is a
// bare name, never a path — so it can never escape the registry root.
var regIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// regKinds maps a Brain registry kind to its subdir + file extension.
//   - workflow      → workflows/<id>.yaml (a flow)
//   - agent         → agents/<id>.yaml    (a persona's manifest: model, skills, tools, role)
//   - agent_persona → agents/<id>.md      (the persona's prompt/instructions)
//   - skill         → skills/<id>.md      (a method fragment injected into personas)
var regKinds = map[string]struct{ sub, ext string }{
	"workflow":      {"workflows", ".yaml"},
	"agent":         {"agents", ".yaml"},
	"agent_persona": {"agents", ".md"},
	"skill":         {"skills", ".md"},
}

func (o EngineOps) regPath(kind, id string) (string, error) {
	k, ok := regKinds[kind]
	if !ok {
		return "", fmt.Errorf("unknown registry kind %q (want workflow|agent|agent_persona|skill)", kind)
	}
	if o.RegistryDir == "" {
		return "", fmt.Errorf("registry dir not configured")
	}
	if !regIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid id %q (must match ^[A-Za-z0-9_-]+$)", id)
	}
	return filepath.Join(o.RegistryDir, k.sub, id+k.ext), nil
}

func (o EngineOps) ListRegistry(kind string) ([]string, error) {
	k, ok := regKinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown registry kind %q", kind)
	}
	entries, err := os.ReadDir(filepath.Join(o.RegistryDir, k.sub))
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, k.ext) {
			ids = append(ids, strings.TrimSuffix(n, k.ext))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (o EngineOps) ReadRegistry(kind, id string) (string, error) {
	p, err := o.regPath(kind, id)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (o EngineOps) WriteRegistry(kind, id, content string) error {
	p, err := o.regPath(kind, id)
	if err != nil {
		return err
	}
	if err := validateRegistryContent(kind, []byte(content)); err != nil {
		return fmt.Errorf("invalid %s %q: %w", kind, id, err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return err
	}
	// A written workflow must invalidate the engine's cache so the NEXT run re-reads
	// the new manifest (same step the HTTP putRegistry handler takes).
	if kind == "workflow" && o.Engine != nil {
		o.Engine.InvalidateWorkflow(id)
	}
	return nil
}

func (o EngineOps) DeleteRegistry(kind, id string) error {
	p, err := o.regPath(kind, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	if kind == "workflow" && o.Engine != nil {
		o.Engine.InvalidateWorkflow(id)
	}
	return nil
}

// validateRegistryContent runs the SAME parser the kernel uses so the Brain can
// never write a manifest the engine would choke on. Markdown kinds (persona/skill)
// are free text — no schema.
func validateRegistryContent(kind string, b []byte) error {
	switch kind {
	case "workflow":
		_, err := workflow.Parse(b)
		return err
	case "agent":
		var m agent.Manifest
		return yaml.Unmarshal(b, &m)
	default: // agent_persona, skill — markdown
		return nil
	}
}
