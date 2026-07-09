// Clasificación design/ejecución de los agentes del registry, derivada como EXCLUSIÓN.
//
// Los agentes de EJECUCIÓN (los "lanes" que escriben o revisan código) se escaffoldean en
// el repo del proyecto (.github/agents) y corren AHÍ, vía Copilot / GitHub Actions — editar
// su copia del registry acá no cambia nada vivo, así que se ocultan del editor de método.
// Todo lo demás son agentes de MÉTODO que el control/conductor corre desde el registry:
//   · diseño     → analyst, pm, architect, ux, designer, scrum-master, data-modeler,
//                  principles, product-advisor
//   · ceremonias → planner (planning), demo-reporter (review), retro-analyst (retro),
//                  story-detailer (groom JIT del conductor), iteration-planner, art-director
// …y esos SÍ se muestran y se editan acá.
//
// Se deriva la EXCLUSIÓN (denylist), no la inclusión: los lanes de código son la familia
// `<stack>-dev` (react-dev, python-dev, flutter-dev, firebase-dev, supabase-dev, …) más
// `dev` / `reviewer` / `verifier` — un conjunto acotado y matcheable. Un denylist falla
// ABIERTO (un lane nuevo se muestra de más pero sigue siendo editable), mucho menos dañino
// que el viejo allowlist estático que fallaba CERRADO y escondía agentes de ceremonia reales
// (planner, demo-reporter, retro-analyst, art-director) además de listar ids inexistentes
// (ux-designer/po/product-owner). El patrón `-dev` cubre stacks futuros sin tocar esta lista.

// isExecutionLane: true si el id es un lane de ejecución (código/review/verify) que vive en
// el repo scaffoldeado, no en el método editable acá.
export function isExecutionLane(id: string): boolean {
  const l = id.toLowerCase();
  return l === "dev" || l.endsWith("-dev") || l === "reviewer" || l === "verifier";
}

// designAgentIds: filtra los ids del registry a los agentes de MÉTODO (los editables acá).
export function designAgentIds(ids: readonly string[]): string[] {
  return ids.filter((id) => !isExecutionLane(id));
}
