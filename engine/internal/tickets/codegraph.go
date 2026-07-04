package tickets

// codegraph — el "cerebro" producto↔código (task #5). El único dato del grafo
// que NO se deriva ya de otra parte son las RUTAS que cada story tocó de verdad
// (story→archivos): stories, deps, sprints y epics ya son nativos en Issues, y
// los símbolos intra-repo los resuelven Copilot/Claude solos. De story→archivos
// se agregan dos vistas útiles: qué stories construyeron un módulo (para
// reutilizar en vez de duplicar) y el mapa de módulos que se inyecta al despacho.

import (
	"fmt"
	"sort"
	"strings"
)

// RecordStoryFiles registra las rutas que el PR mergeado de storyID tocó.
// Idempotente por (project, story, path) — re-proyectar el mismo merge es no-op.
// Devuelve cuántas filas nuevas insertó. paths vacío es no-op.
func (s *Store) RecordStoryFiles(projectID, storyID string, paths []string) (int, error) {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	if storyID == "" || len(paths) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO story_files(project_id, story_id, path) VALUES(?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	inserted := 0
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		r, err := stmt.Exec(projectID, storyID, p)
		if err != nil {
			return 0, fmt.Errorf("record story file %s/%s: %w", storyID, p, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

// FilesForStory devuelve las rutas registradas de una story, orden estable.
func (s *Store) FilesForStory(projectID, storyID string) ([]string, error) {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	rows, err := s.db.Query(
		`SELECT path FROM story_files WHERE project_id=? AND story_id=? ORDER BY path`,
		projectID, storyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ModuleHit es un módulo (directorio) del mapa producto↔código: qué stories y
// lanes lo construyeron y cuántos archivos suyos se han tocado.
type ModuleHit struct {
	Dir     string   `json:"dir"`
	Files   int      `json:"files"`
	Stories []string `json:"stories"`
	Lanes   []string `json:"lanes"`
}

// ModuleMap agrega story_files a nivel de directorio (los primeros `depth`
// segmentos de ruta) para el proyecto. Es el "repo map" barato de Forja,
// construido del historial de PRs mergeados en vez de tree-sitter: por cada
// módulo, qué stories/lanes lo tocaron y cuántos archivos. Ordenado por nº de
// archivos desc, luego por nombre — determinista para test e inyección.
// depth<=0 se trata como 2.
func (s *Store) ModuleMap(projectID string, depth int) ([]ModuleHit, error) {
	if projectID == "" {
		projectID = DefaultProjectID
	}
	if depth <= 0 {
		depth = 2
	}
	rows, err := s.db.Query(`
		SELECT sf.path, sf.story_id, COALESCE(st.owner,'')
		FROM story_files sf
		LEFT JOIN stories st ON st.id = sf.story_id AND st.project_id = sf.project_id
		WHERE sf.project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type acc struct {
		files   int
		stories map[string]bool
		lanes   map[string]bool
	}
	byDir := map[string]*acc{}
	for rows.Next() {
		var path, story, owner string
		if err := rows.Scan(&path, &story, &owner); err != nil {
			return nil, err
		}
		dir := moduleDir(path, depth)
		a := byDir[dir]
		if a == nil {
			a = &acc{stories: map[string]bool{}, lanes: map[string]bool{}}
			byDir[dir] = a
		}
		a.files++
		if story != "" {
			a.stories[story] = true
		}
		if owner != "" {
			a.lanes[owner] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ModuleHit, 0, len(byDir))
	for dir, a := range byDir {
		out = append(out, ModuleHit{
			Dir:     dir,
			Files:   a.files,
			Stories: sortedKeys(a.stories),
			Lanes:   sortedKeys(a.lanes),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}

// moduleDir reduce una ruta de archivo a su módulo: los primeros `depth`
// segmentos de directorio (sin el nombre de archivo). Un archivo en la raíz
// cae en "/".
func moduleDir(path string, depth int) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) <= 1 {
		return "/" // archivo en la raíz del repo
	}
	dirSegs := segs[:len(segs)-1] // quita el nombre de archivo
	if len(dirSegs) > depth {
		dirSegs = dirSegs[:depth]
	}
	return strings.Join(dirSegs, "/")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
