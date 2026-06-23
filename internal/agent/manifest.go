package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Manifest is an agent definition: DATA (registry/agents/<id>.yaml) + a persona
// markdown file (registry/agents/<id>.md). The kernel never hardcodes an agent;
// it loads the manifest and feeds it to the Backend.
type Manifest struct {
	ID      string   `yaml:"id"`
	Version string   `yaml:"version"`
	Model   string   `yaml:"model"`
	Skills  []string `yaml:"skills"`
	Tools   []string `yaml:"tools"` // logical tool allowlist: read|edit|write|bash
	Role    string   `yaml:"role"`

	Persona string `yaml:"-"` // loaded from <id>.md
}

// Loader loads agent manifests by id.
type Loader interface {
	Load(id string) (*Manifest, error)
}

// DirLoader loads agent manifests from a registry directory.
type DirLoader struct{ Root string }

// NewDirLoader builds a manifest loader rooted at dir (e.g. registry/agents).
func NewDirLoader(dir string) *DirLoader { return &DirLoader{Root: dir} }

func (d *DirLoader) Load(id string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(d.Root, id+".yaml"))
	if err != nil {
		return nil, fmt.Errorf("load agent %s: %w", id, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse agent %s: %w", id, err)
	}
	if m.ID == "" {
		m.ID = id
	}
	// Persona is optional; missing .md is not fatal.
	if persona, err := os.ReadFile(filepath.Join(d.Root, id+".md")); err == nil {
		m.Persona = string(persona)
	}
	return &m, nil
}

// MapLoader serves manifests from memory (tests).
type MapLoader map[string]*Manifest

func (m MapLoader) Load(id string) (*Manifest, error) {
	mf, ok := m[id]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", id)
	}
	return mf, nil
}

// toolMap translates the logical tool allowlist to claude CLI tool names.
var toolMap = map[string]string{
	"read":  "Read",
	"edit":  "Edit",
	"write": "Write",
	"bash":  "Bash",
	"grep":  "Grep",
	"glob":  "Glob",
}

// AllowedTools returns the claude tool names for the manifest's logical tools.
func (m *Manifest) AllowedTools() []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		if mapped, ok := toolMap[t]; ok {
			out = append(out, mapped)
		} else {
			out = append(out, t) // pass through already-canonical names
		}
	}
	return out
}
