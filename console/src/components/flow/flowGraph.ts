// flowGraph — derivación PURA (sin JSX, sin React Flow) del grafo de la vista Flow.
// El proyecto entero como modelo: las fases del diseño con sus gates, el handoff,
// y los sprints con sus stories, deps y ceremonias intercaladas. FlowView toma este
// modelo y lo posiciona (dagre). Todo lo verificable —bandas, sellos por modo,
// ceremonias por feature-detect, answered≠approved, última instancia ante steps
// duplicados— vive acá y se testea sin montar el componente (flowGraph.test.ts).

import type {
  DesignPhase,
  DesignStepStatus,
  OrchestratorTicket,
  Sprint,
  TicketStatus,
} from "@/lib/types";
import type { PhaseState } from "@/components/studio/phaseHelpers";

// phaseState se INYECTA (no se importa como valor) para que este módulo quede sin
// dependencias de runtime — así el test (node:test + type-stripping, que no resuelve
// el alias @/) puede ejercitar la derivación sin montar nada. FlowView y el test le
// pasan el phaseState REAL de phaseHelpers: se reusa la lógica del fix #22, no se
// reimplementa.
export type PhaseStateFn = (p: DesignPhase) => PhaseState;

// ─────────────────────────────────────────────────────────────────────────────
// Bandas y constantes estructurales
// ─────────────────────────────────────────────────────────────────────────────

export type Band = "design" | "handoff" | "execution";

// Fases que caen en la banda HANDOFF (docs_pr → handoff). Es estructural —como
// BACKLOG_STEPS en phaseHelpers—, NO "la lista de fases" (esa se deriva de los
// steps del run): solo el par de pasos que separa diseño de ejecución.
export const HANDOFF_STEPS = new Set(["docs_pr", "handoff"]);

export type CeremonyMode = "auto" | "ceremony";
export type CeremonyKind = "planning" | "review" | "retro";
export type SealState = "done" | "running" | "pending";

export interface CeremonyModes {
  planning: CeremonyMode;
  review: CeremonyMode;
  retro: CeremonyMode;
}

// ─────────────────────────────────────────────────────────────────────────────
// Descriptores de nodo/arista (agnósticos a React Flow)
// ─────────────────────────────────────────────────────────────────────────────

export interface PhaseNodeDesc {
  id: string; // "phase:<stepId>"
  band: "design" | "handoff";
  stepId: string;
  name: string;
  state: PhaseState; // vía phaseState() — jamás leer gateStatus directo (bug #22)
  hasGate: boolean;
}

export interface GateNodeDesc {
  id: string; // "gate:<stepId>"
  band: "design" | "handoff";
  stepId: string; // la fase a la que pertenece el gate
  gateId: string;
  name: string;
  // Mismo phaseState() que la fase: un gate "answered" (gateStatus DONE + fase
  // re-corriendo) reporta "running", NO "approved" — el fix #22, reusado.
  state: PhaseState;
}

export interface StoryNodeDesc {
  id: string; // "story:<ticketId>"
  ticketId: string;
  sprintId: string;
  title: string;
  status: TicketStatus;
  owner?: string;
  kind: "story" | "bug";
  screenKey?: string;
  runId?: string;
}

export interface SprintSeals {
  // undefined = no se renderiza (modo "auto"). Presente solo en modo "ceremony".
  planning?: SealState;
  review?: SealState;
  retro?: SealState;
}

export interface SprintGroupDesc {
  id: string; // sprint id (SP1)
  name: string;
  goal: string;
  order: number; // clave de orden numérico (SP2 → 2)
  seals: SprintSeals;
  stories: StoryNodeDesc[];
}

export interface CeremonyNodeDesc {
  id: string; // "ceremony:<kind>:<sprintId>"
  kind: CeremonyKind;
  sprintId: string;
  order: number; // orden del sprint al que pertenece (para la ubicación entre columnas)
  runId: string; // *_run_id — feature-detect: solo existe si el run existe
  state: SealState; // done (*_at>0) | running (run en vuelo)
  at: number | null; // *_at (epoch) cuando la ceremonia se selló — para el sello con fecha
  accepted: boolean; // review con reviewed_at>0 → preview disponible (#23)
}

export interface FlowEdgeDesc {
  id: string;
  source: string; // node id
  target: string; // node id
  crossSprint: boolean; // dep que cruza sprints → arista punteada
  depStatus: TicketStatus; // estado de la dependencia (colorea la arista)
  animated: boolean;
}

export interface FlowModel {
  designPhases: PhaseNodeDesc[];
  handoffPhases: PhaseNodeDesc[];
  gates: GateNodeDesc[];
  sprints: SprintGroupDesc[];
  ceremonies: CeremonyNodeDesc[];
  storyEdges: FlowEdgeDesc[];
  hasDesign: boolean;
  hasExecution: boolean;
}

export interface FlowInput {
  phases: DesignPhase[]; // DesignRun.phases (ya resueltas por instancia: bug #18)
  sprints: Sprint[];
  tickets: OrchestratorTicket[];
  modes: CeremonyModes;
  stateOf: PhaseStateFn; // phaseState real de phaseHelpers (fix #22)
}

// ─────────────────────────────────────────────────────────────────────────────
// Última instancia ante steps duplicados (bug #18) — mismo criterio que el
// statusOf de mapDesignRun (api.ts). Se exporta y testea acá para documentar y
// blindar la regla en la capa de derivación de Flow.
// ─────────────────────────────────────────────────────────────────────────────

export interface RawStep {
  step_id: string;
  status: DesignStepStatus;
}

export interface PhaseDef {
  stepId: string;
  name: string;
  gateId?: string;
}

// La instancia MÁS RECIENTE gana: un retry deja una task vieja FAILED + una nueva
// DONE; los steps llegan created_at ASC, así que la última coincidencia es el
// intento actual. find() (primera) reportaría la FAILED rancia.
export function latestInstanceStatus(steps: RawStep[], stepId: string): DesignStepStatus {
  let found: DesignStepStatus | undefined;
  for (const s of steps) {
    if (s.step_id === stepId) found = s.status;
  }
  return found ?? "QUEUED";
}

// Construye DesignPhase[] desde defs + steps aplicando la regla de última instancia.
// Espeja mapDesignRun; existe para testear el caso de steps duplicados de punta a
// punta (steps → nodos) sin fetch ni el componente.
export function phasesFromSteps(defs: PhaseDef[], steps: RawStep[]): DesignPhase[] {
  return defs.map((d) => ({
    stepId: d.stepId,
    name: d.name,
    gateId: d.gateId,
    designStatus: latestInstanceStatus(steps, d.stepId),
    gateStatus: d.gateId ? latestInstanceStatus(steps, d.gateId) : "QUEUED",
  }));
}

// ─────────────────────────────────────────────────────────────────────────────
// Fases → nodos de fase + gate
// ─────────────────────────────────────────────────────────────────────────────

function bandForPhase(stepId: string): "design" | "handoff" {
  return HANDOFF_STEPS.has(stepId) ? "handoff" : "design";
}

export function buildPhaseNodes(
  phases: DesignPhase[],
  stateOf: PhaseStateFn,
): {
  design: PhaseNodeDesc[];
  handoff: PhaseNodeDesc[];
  gates: GateNodeDesc[];
} {
  const design: PhaseNodeDesc[] = [];
  const handoff: PhaseNodeDesc[] = [];
  const gates: GateNodeDesc[] = [];
  for (const p of phases) {
    const band = bandForPhase(p.stepId);
    const state = stateOf(p);
    const node: PhaseNodeDesc = {
      id: `phase:${p.stepId}`,
      band,
      stepId: p.stepId,
      name: p.name,
      state,
      hasGate: !!p.gateId,
    };
    (band === "handoff" ? handoff : design).push(node);
    if (p.gateId) {
      gates.push({
        id: `gate:${p.stepId}`,
        band,
        stepId: p.stepId,
        gateId: p.gateId,
        name: p.name,
        state, // mismo phaseState → answered gate = running, no approved
      });
    }
  }
  return { design, handoff, gates };
}

// ─────────────────────────────────────────────────────────────────────────────
// Sprints → grupos con stories + sellos de ceremonia
// ─────────────────────────────────────────────────────────────────────────────

// Orden numérico del sprint: extrae el número final del id ("SP12" → 12). Sin
// dígitos → Infinity (al final, estable).
export function sprintOrder(id: string): number {
  const m = id.match(/(\d+)\s*$/);
  return m ? parseInt(m[1], 10) : Number.POSITIVE_INFINITY;
}

function sealState(at: number | undefined, runId: string | undefined): SealState {
  if (at && at > 0) return "done";
  if (runId) return "running"; // run arrancó pero aún no aplicó → en curso
  return "pending";
}

// Sellos del sprint: solo se computan para los modos en "ceremony"; en "auto" el
// campo queda undefined y FlowView no lo pinta.
export function sprintSeals(sprint: Sprint | undefined, modes: CeremonyModes): SprintSeals {
  const seals: SprintSeals = {};
  if (modes.planning === "ceremony") {
    seals.planning = sealState(sprint?.planned_at, sprint?.planning_run_id);
  }
  if (modes.review === "ceremony") {
    seals.review = sealState(sprint?.reviewed_at, sprint?.review_run_id);
  }
  if (modes.retro === "ceremony") {
    seals.retro = sealState(sprint?.retro_at, sprint?.retro_run_id);
  }
  return seals;
}

function toStoryNode(t: OrchestratorTicket): StoryNodeDesc {
  return {
    id: `story:${t.id}`,
    ticketId: t.id,
    sprintId: t.sprint_id || "",
    title: t.title,
    status: t.status,
    owner: t.owner,
    kind: t.kind === "bug" ? "bug" : "story",
    screenKey: t.screen_key || undefined,
    runId: t.run_id,
  };
}

export function buildSprintGroups(
  sprints: Sprint[],
  tickets: OrchestratorTicket[],
  modes: CeremonyModes,
): SprintGroupDesc[] {
  const metaById = new Map(sprints.map((s) => [s.id, s]));
  // Todos los sprint ids: los de la lista + los referenciados por las stories (una
  // story puede apuntar a un sprint que el /sprints aún no listó).
  const ids = new Set<string>();
  for (const s of sprints) ids.add(s.id);
  for (const t of tickets) if (t.sprint_id) ids.add(t.sprint_id);

  const groups: SprintGroupDesc[] = [];
  for (const id of ids) {
    const meta = metaById.get(id);
    const stories = tickets.filter((t) => (t.sprint_id || "") === id).map(toStoryNode);
    groups.push({
      id,
      name: meta?.name || id,
      goal: meta?.goal || "",
      order: sprintOrder(id),
      seals: sprintSeals(meta, modes),
      stories,
    });
  }
  groups.sort((a, b) => a.order - b.order || a.id.localeCompare(b.id));
  return groups;
}

// ─────────────────────────────────────────────────────────────────────────────
// Ceremonias entre sprints — feature-detect PURO por *_run_id (nada hardcodeado):
// un nodo existe iff el run de esa ceremonia existe. El modo del proyecto gobierna
// los SELLOS del header, no la existencia del run (que ya ocurrió).
// ─────────────────────────────────────────────────────────────────────────────

export function buildCeremonyNodes(sprints: Sprint[]): CeremonyNodeDesc[] {
  const out: CeremonyNodeDesc[] = [];
  for (const s of sprints) {
    const order = sprintOrder(s.id);
    if (s.planning_run_id) {
      out.push({
        id: `ceremony:planning:${s.id}`,
        kind: "planning",
        sprintId: s.id,
        order,
        runId: s.planning_run_id,
        state: sealState(s.planned_at, s.planning_run_id),
        at: s.planned_at && s.planned_at > 0 ? s.planned_at : null,
        accepted: false,
      });
    }
    if (s.review_run_id) {
      out.push({
        id: `ceremony:review:${s.id}`,
        kind: "review",
        sprintId: s.id,
        order,
        runId: s.review_run_id,
        state: sealState(s.reviewed_at, s.review_run_id),
        at: s.reviewed_at && s.reviewed_at > 0 ? s.reviewed_at : null,
        accepted: !!(s.reviewed_at && s.reviewed_at > 0), // review aceptado → preview (#23)
      });
    }
    if (s.retro_run_id) {
      out.push({
        id: `ceremony:retro:${s.id}`,
        kind: "retro",
        sprintId: s.id,
        order,
        runId: s.retro_run_id,
        state: sealState(s.retro_at, s.retro_run_id),
        at: s.retro_at && s.retro_at > 0 ? s.retro_at : null,
        accepted: false,
      });
    }
  }
  return out;
}

// ─────────────────────────────────────────────────────────────────────────────
// Aristas de dependencia entre stories (reusa la derivación de DepGraph):
// source = dependencia, target = dependiente; cruza-sprint → punteada.
// ─────────────────────────────────────────────────────────────────────────────

export function buildStoryEdges(tickets: OrchestratorTicket[]): FlowEdgeDesc[] {
  const byId = new Map(tickets.map((t) => [t.id, t]));
  const edges: FlowEdgeDesc[] = [];
  for (const t of tickets) {
    for (const depId of t.deps ?? []) {
      const dep = byId.get(depId);
      if (!dep) continue; // dep colgante = sin arista (igual que DepGraph)
      const crossSprint =
        !!t.sprint_id && !!dep.sprint_id && t.sprint_id !== dep.sprint_id;
      edges.push({
        id: `${depId}->${t.id}`,
        source: `story:${depId}`,
        target: `story:${t.id}`,
        crossSprint,
        depStatus: dep.status,
        animated: t.status === "running" || dep.status === "running",
      });
    }
  }
  return edges;
}

// ─────────────────────────────────────────────────────────────────────────────
// Composición
// ─────────────────────────────────────────────────────────────────────────────

export function buildFlow(input: FlowInput): FlowModel {
  const { design, handoff, gates } = buildPhaseNodes(input.phases, input.stateOf);
  const sprints = buildSprintGroups(input.sprints, input.tickets, input.modes);
  const ceremonies = buildCeremonyNodes(input.sprints);
  const storyEdges = buildStoryEdges(input.tickets);
  return {
    designPhases: design,
    handoffPhases: handoff,
    gates,
    sprints,
    ceremonies,
    storyEdges,
    hasDesign: design.length > 0 || handoff.length > 0,
    hasExecution: sprints.some((s) => s.stories.length > 0),
  };
}
