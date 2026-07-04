package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BackendDef is the YAML schema for registry/backends/*.yaml.
// Adding a new CLI engine = creating one YAML file, zero Go.
type BackendDef struct {
	ID       string   `yaml:"id"`
	BaseArgv []string `yaml:"argv"`
}

// LoadBackends scans dir/*.yaml and returns a Backend per file, keyed by ID.
// Unknown / malformed entries are skipped with a log line (non-fatal). Returns
// an empty map (not an error) when dir does not exist — the default CliBackend
// (claude argv) handles the case where no registry is configured.
func LoadBackends(dir string) (map[string]Backend, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	out := make(map[string]Backend, len(entries))
	for _, p := range entries {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("load backend %s: %w", p, err)
		}
		var def BackendDef
		if err := yaml.Unmarshal(b, &def); err != nil {
			log.Printf("backendloader: skip %s: %v", p, err)
			continue
		}
		if def.ID == "" || len(def.BaseArgv) == 0 {
			log.Printf("backendloader: skip %s: missing id or argv", p)
			continue
		}
		out[def.ID] = CliBackend{BaseArgv: def.BaseArgv}
		log.Printf("backendloader: registered backend %q → %v", def.ID, def.BaseArgv)
	}
	return out, nil
}
