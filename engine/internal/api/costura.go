package api

// La COSTURA del journey (hallazgo #1 de la simulación): al publicarse el
// backlog de un diseño, encadenar automáticamente lo que antes eran dos
// botones manuales — exportar el backlog a GitHub Issues (con dependencias
// nativas) y hornear el scaffold (agentes + checks) en el repo. El usuario
// pasa de "chuleta de 3 pasos" a "aprobar el diseño y ver la fábrica lista".

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"forge/internal/export"
	"forge/internal/scaffold"
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
