package workflow

import (
	"fmt"
	"path/filepath"
	"sync"
)

// MapLoader serves pre-parsed workflows from memory (tests, embedding).
type MapLoader map[string]*Workflow

func (m MapLoader) Load(id string) (*Workflow, error) {
	wf, ok := m[id]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}
	return wf, nil
}

// DirLoader loads registry/workflows/<id>.yaml from disk and caches the parse.
// This is how the kernel reads methodology-as-data: change the YAML, change the
// flow — no recompile.
type DirLoader struct {
	Root  string // e.g. "registry/workflows"
	mu    sync.Mutex
	cache map[string]*Workflow
}

// NewDirLoader builds a directory-backed loader rooted at dir.
func NewDirLoader(dir string) *DirLoader {
	return &DirLoader{Root: dir, cache: map[string]*Workflow{}}
}

func (d *DirLoader) Load(id string) (*Workflow, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if wf, ok := d.cache[id]; ok {
		return wf, nil
	}
	wf, err := ParseFile(filepath.Join(d.Root, id+".yaml"))
	if err != nil {
		return nil, err
	}
	d.cache[id] = wf
	return wf, nil
}
