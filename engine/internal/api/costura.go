package api

// La COSTURA del journey (hallazgo #1 de la simulación): al publicarse el
// backlog de un diseño, encadenar automáticamente lo que antes eran dos
// botones manuales — exportar el backlog a GitHub Issues (con dependencias
// nativas) y hornear el scaffold (agentes + checks) en el repo. El usuario
// pasa de "chuleta de 3 pasos" a "aprobar el diseño y ver la fábrica lista".

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"forge/internal/export"
	"forge/internal/scaffold"
	"forge/internal/tickets"
)

// OnBacklogPublished corre tras el handoff del diseño (hook del PublishRunner,
// en goroutine). Best-effort con log claro: si un paso falla, los botones
// manuales siguen existiendo como plan B.
func (s *Server) OnBacklogPublished(projectID, repoURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	gh := s.ghFor(ctx, projectID)

	// 1) Exportar backlog → GitHub Issues + deps nativas.
	res, err := export.GitHubBacklog(ctx, s.Tickets, gh, projectID, repoURL)
	if err != nil {
		log.Printf("costura(%s): export a GitHub falló (queda el botón manual): %v", projectID, err)
	} else {
		log.Printf("costura(%s): backlog exportado — %d issues, %d deps", projectID, res.IssuesCreated, res.DepsCreated)
	}

	// 2) Scaffold: agentes + instrucciones + checks al repo.
	stack := s.stackForProject(projectID)
	vars := s.scaffoldVarsFor(projectID, stack)
	files, missing, err := scaffold.Render(scaffoldTemplatesDir(), stack, vars)
	if err != nil {
		log.Printf("costura(%s): render del scaffold falló: %v", projectID, err)
		return
	}
	if len(missing) > 0 {
		log.Printf("costura(%s): scaffold con vars sin valor: %s", projectID, strings.Join(missing, ", "))
	}
	written, skipped, err := scaffold.Apply(ctx, gh, repoURL, "main", files, "chore(scaffold): especialización github-native (auto, post-diseño)")
	if err != nil {
		log.Printf("costura(%s): apply del scaffold falló (queda el botón manual): %v", projectID, err)
		return
	}
	log.Printf("costura(%s): scaffold aplicado — %d escritos, %d al día (stack %s)", projectID, written, skipped, stack)
}

// OnStoryCreated corre tras crear una story a mano (POST /stories): encadena el
// mismo export idempotente de la costura (issue + deps nativas) para que el
// conductor github-native pueda despacharla — sin esto una story manual jamás
// llegaba a GitHub Issues. Best-effort: si el export falla, la story queda
// creada y el botón manual de export sigue como plan B. El handler la invoca
// con `go` (como publish.go hace con OnPublished).
func (s *Server) OnStoryCreated(st tickets.Story) {
	repo := s.exportRepoFor(st)
	if repo == "" {
		return // proyecto default / sin repo GitHub: no hay a dónde exportar
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	s.exportStoryBacklog(ctx, s.ghFor(ctx, st.ProjectID), st, repo)
}

// exportStoryBacklog es el cuerpo testeable de OnStoryCreated: reusa
// export.GitHubBacklog, idempotente por external_ref — las stories ya
// exportadas se saltan, así que solo viajan la nueva y sus deps. Devuelve el
// resultado real para que el endpoint síncrono POST /stories/{id}/export lo
// exponga; OnStoryCreated (best-effort) descarta el retorno.
func (s *Server) exportStoryBacklog(ctx context.Context, gh export.GitHubWriter, st tickets.Story, repo string) (export.Result, error) {
	res, err := export.GitHubBacklog(ctx, s.Tickets, gh, st.ProjectID, repo)
	if err != nil {
		log.Printf("costura(%s): export de la story %s a GitHub falló (queda el botón manual): %v", st.ProjectID, st.ID, err)
		return res, err
	}
	log.Printf("costura(%s): story %s exportada — %d issues nuevos, %d deps", st.ProjectID, st.ID, res.IssuesCreated, res.DepsCreated)
	return res, nil
}

// exportRepoFor resuelve el repo GitHub destino de una story creada a mano: el
// de la story si lo trae, si no el del proyecto. El proyecto "default" (legacy,
// sin repo real) no exporta.
func (s *Server) exportRepoFor(st tickets.Story) string {
	if st.ProjectID == "" || st.ProjectID == tickets.DefaultProjectID {
		return ""
	}
	if st.Repo != "" {
		return st.Repo
	}
	if s.Projects == nil {
		return ""
	}
	p, err := s.Projects.Get(st.ProjectID)
	if err != nil {
		return ""
	}
	return p.Repo
}

// WriteModuleMap regenera docs/MODULE_MAP.md desde el grafo producto↔código
// (task #5, Capa 1 "mantenida fresca") y lo escribe al repo. Se dispara cuando
// la proyección captura archivos nuevos de un PR mergeado (Projector.OnGraphChanged).
// El iteration-planner lo lee para reutilizar módulos en vez de crear estructura
// paralela. Best-effort y con su propio ctx (como OnStoryCreated): un mapa vacío
// no escribe archivo, y WriteFile es idempotente (no commitea si no cambió).
func (s *Server) WriteModuleMap(projectID, repoURL string) {
	if s.Tickets == nil || repoURL == "" {
		return
	}
	mods, err := s.Tickets.ModuleMap(projectID, 2)
	if err != nil {
		log.Printf("costura(%s): module map — leer grafo: %v", projectID, err)
		return
	}
	content := renderModuleMap(mods)
	if content == "" {
		return // sin módulos todavía: no escribimos un archivo vacío
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files := []scaffold.File{{Path: "docs/MODULE_MAP.md", Content: content}}
	if _, _, err := scaffold.Apply(ctx, s.ghFor(ctx, projectID), repoURL, "main", files,
		"chore(map): actualizar mapa de módulos (grafo producto↔código)"); err != nil {
		log.Printf("costura(%s): module map — escribir docs/MODULE_MAP.md: %v", projectID, err)
	}
}

// renderModuleMap compone docs/MODULE_MAP.md desde el mapa de módulos del grafo,
// most-touched primero. Vacío (proyecto sin merges) → "" para que WriteModuleMap
// no escriba un archivo vacío. Función pura → testeable sin GitHub.
func renderModuleMap(mods []tickets.ModuleHit) string {
	if len(mods) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Module map\n\n")
	b.WriteString("Módulos ya construidos en este repo, derivados de los archivos que tocaron los " +
		"PRs mergeados (más tocados primero). Lo mantiene aiuda-forge automáticamente tras cada " +
		"merge — al planear una iteración, EXTIENDE estos módulos en vez de crear estructura paralela.\n\n")
	b.WriteString("| Módulo | Archivos | Lanes | Stories |\n|---|---|---|---|\n")
	for _, m := range mods {
		fmt.Fprintf(&b, "| `%s/` | %d | %s | %s |\n",
			m.Dir, m.Files, strings.Join(m.Lanes, ", "), strings.Join(m.Stories, ", "))
	}
	return b.String()
}

// stackForProject deduce el stack por las lanes reales del backlog:
// cualquier lane flutter → stack flutter; si no, el default python+react.
func (s *Server) stackForProject(projectID string) string {
	if s.Tickets != nil {
		if stories, err := s.Tickets.ListStoriesByProject(projectID); err == nil {
			for _, st := range stories {
				if strings.Contains(strings.ToLower(st.Owner), "flutter") {
					return "aiuda-flutter-firebase"
				}
			}
		}
	}
	return "python-fastapi-react"
}

// scaffoldVarsFor deriva las variables de template del proyecto (nombre, lanes).
func (s *Server) scaffoldVarsFor(projectID, stack string) scaffold.Vars {
	vars := scaffold.Vars{"stack": stack, "language": "es"}
	if s.Projects != nil {
		if p, err := s.Projects.Get(projectID); err == nil {
			vars["project_name"] = p.Name
		}
	}
	if s.Tickets != nil {
		if stories, err := s.Tickets.ListStoriesByProject(projectID); err == nil {
			seen := map[string]bool{}
			var lanes []string
			for _, st := range stories {
				if st.Owner != "" && !seen[st.Owner] {
					seen[st.Owner] = true
					lanes = append(lanes, st.Owner)
				}
			}
			sort.Strings(lanes)
			vars["lanes"] = strings.Join(lanes, ", ")
		}
	}
	return vars
}
