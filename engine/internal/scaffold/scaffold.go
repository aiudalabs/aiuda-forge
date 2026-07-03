// Package scaffold renders the GitHub-native repo templates
// (registry/templates/github-native) for a given stack and writes the rendered
// files into a destination repository. It is pure data-in/data-out: it knows
// nothing about methodology — the templates carry it — and does only plain
// string substitution of {{variables}}.
package scaffold

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// File is a rendered file ready to write to the destination repo.
type File struct {
	Path    string // destination path in the repo (e.g. ".github/agents/python-dev.agent.md")
	Content string
}

// Vars are the {{name}} substitution variables of the templates.
type Vars map[string]string

const (
	commonDir  = "_common"
	tmplSuffix = ".tmpl"
)

// varPattern matches an unresolved template variable such as {{project_name}}.
var varPattern = regexp.MustCompile(`\{\{([a-z_]+)\}\}`)

// Render loads the templates under templatesDir for the given stack and returns
// the rendered files: first _common/, then <stack>/ (a path present in both is
// won by the stack's version). Template files end in .tmpl and the suffix is
// stripped from the destination Path. A {{k}} occurrence is replaced by Vars[k];
// a variable with no value in Vars is left intact in the content (not an error —
// it can be filled in later) but is reported in missing (deduped, sorted).
func Render(templatesDir, stack string, vars Vars) (files []File, missing []string, err error) {
	stacks, err := availableStacks(templatesDir)
	if err != nil {
		return nil, nil, err
	}
	if !contains(stacks, stack) {
		return nil, nil, fmt.Errorf("scaffold: unknown stack %q; available: %s", stack, strings.Join(stacks, ", "))
	}

	// Ordered merge: _common first, then the stack overrides/appends by path.
	order := []string{}
	byPath := map[string]string{}
	for _, root := range []string{filepath.Join(templatesDir, commonDir), filepath.Join(templatesDir, stack)} {
		rendered, rerr := renderDir(root, vars)
		if rerr != nil {
			return nil, nil, rerr
		}
		for _, f := range rendered {
			if _, seen := byPath[f.Path]; !seen {
				order = append(order, f.Path)
			}
			byPath[f.Path] = f.Content
		}
	}

	missingSet := map[string]bool{}
	for _, p := range order {
		content := byPath[p]
		files = append(files, File{Path: p, Content: content})
		for _, m := range varPattern.FindAllStringSubmatch(content, -1) {
			missingSet[m[1]] = true
		}
	}
	for name := range missingSet {
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return files, missing, nil
}

// renderDir walks a single template root (an absolute or relative directory),
// substituting variables and stripping the .tmpl suffix. A missing root is not
// an error (a stack may have no _common overrides); the caller controls which
// roots exist.
func renderDir(root string, vars Vars) ([]File, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scaffold: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("scaffold: %s is not a directory", root)
	}

	var files []File
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "README.md" || !strings.HasSuffix(d.Name(), tmplSuffix) {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		files = append(files, File{
			Path:    filepath.ToSlash(strings.TrimSuffix(rel, tmplSuffix)),
			Content: substitute(string(raw), vars),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scaffold: render %s: %w", root, err)
	}
	return files, nil
}

// substitute does a plain string replace of "{{k}}" -> v for each var; it does
// NOT use text/template because the .tmpl files contain YAML/markdown with
// braces that must not be interpreted.
func substitute(content string, vars Vars) string {
	for k, v := range vars {
		content = strings.ReplaceAll(content, "{{"+k+"}}", v)
	}
	return content
}

// availableStacks lists the stack directories under templatesDir (every
// subdirectory except _common), sorted.
func availableStacks(templatesDir string) ([]string, error) {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		return nil, fmt.Errorf("scaffold: read templates dir %s: %w", templatesDir, err)
	}
	var stacks []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != commonDir {
			stacks = append(stacks, e.Name())
		}
	}
	sort.Strings(stacks)
	return stacks, nil
}

// FileWriter is the write surface (implemented by *github.Client.WriteFile).
type FileWriter interface {
	WriteFile(ctx context.Context, repoURL, branch, path, content, message string) (bool, error)
}

// Apply writes the files to the repo on the given branch, returning how many
// were written and how many were already identical (WriteFile returns false when
// the content did not change). It fails fast on the first error. The per-file
// commit message is commitPrefix + ": " + path.
func Apply(ctx context.Context, w FileWriter, repoURL, branch string, files []File, commitPrefix string) (written, skipped int, err error) {
	for _, f := range files {
		changed, werr := w.WriteFile(ctx, repoURL, branch, f.Path, f.Content, commitPrefix+": "+f.Path)
		if werr != nil {
			return written, skipped, fmt.Errorf("scaffold: write %s: %w", f.Path, werr)
		}
		if changed {
			written++
			continue
		}
		skipped++
	}
	return written, skipped, nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
