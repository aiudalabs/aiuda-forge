// cycleModel — derivación PURA de la vista CICLO de /flow. Toma el MISMO FlowModel
// que el grafo (flowGraph.ts) y lo proyecta sobre las estaciones fijas del diagrama
// "00 · El ciclo Scrum de Fluxo" (viewBox 940×560): las 6 fases del pipeline de
// diseño de la banda izquierda, el product backlog, el sprint activo (nombre +
// contadores done/running/failed), las tres ceremonias (planning/review/retro con
// atenuado en modo auto) y el incremento con su preview. Sin JSX ni React Flow: es
// derivación verificable con node:test (cycleModel.test.ts). NO reimplementa nada —
// consume PhaseNodeDesc/SprintGroupDesc/CeremonyNodeDesc/CeremonyModes de flowGraph.

import type {
  CeremonyKind,
  CeremonyModes,
  FlowModel,
  PhaseNodeDesc,
  SprintGroupDesc,
} from "../flowGraph";
import type { PhaseState } from "@/components/studio/phaseHelpers";

// ─────────────────────────────────────────────────────────────────────────────
// Estados de estación (lenguaje visual del diagrama, re-mapeado a estado VIVO):
//   done    → sólido naranja (era "existe")
//   running → naranja con stroke animado (marcha de hormigas)
//   awaiting→ ámbar pulsando (gate esperando al humano)
//   pending → azul punteado (era "propuesto / aún no corrido")
//   failed  → rojo sólido
// ─────────────────────────────────────────────────────────────────────────────

export type StationState = "done" | "running" | "awaiting" | "pending" | "failed";

// Las 6 líneas de la banda STUDIO/DISEÑO. Cada una agrega uno o varios stepIds del
// design.yaml canónico (prd cubre validate_prd+data_model; ui cubre mockups; etc.).
// Es un mapa de PRESENTACIÓN de la vista —como HANDOFF_STEPS en flowGraph—, no
// metodología: un stepId ausente simplemente no aporta (la línea cae a pending).
export type StageKey = "brief" | "constitution" | "prd" | "architecture" | "ui" | "backlog";

const STAGE_STEPS: Record<StageKey, string[]> = {
  brief: ["discovery"],
  constitution: ["constitution"],
  prd: ["prd", "validate_prd", "data_model"],
  architecture: ["architecture"],
  ui: ["ui", "mockups"],
  backlog: ["backlog", "validate_backlog"],
};

export const STAGE_ORDER: StageKey[] = ["brief", "constitution", "prd", "architecture", "ui", "backlog"];

export interface CycleStage {
  key: StageKey;
  state: StationState;
  target: string | null; // "phase:<stepId>" de la fase a abrir (la activa del grupo)
}

// Colapsa un PhaseState (5 valores de phaseHelpers) al StationState de la estación.
function stationFromPhase(s: PhaseState): StationState {
  return s === "approved" ? "done" : s; // pending/running/awaiting/failed calzan 1:1
}

// Agrega los estados de las fases mapeadas a una línea. Prioridad: lo que exige
// acción/atención primero (running > awaiting > failed), luego progreso parcial
// (algo aprobado y algo pendiente = activo/running), luego todo-aprobado = done,
// y si no hay nada corrido = pending. Determinista y sin sorpresas.
function aggregate(states: PhaseState[]): StationState {
  if (states.length === 0) return "pending";
  if (states.includes("running")) return "running";
  if (states.includes("awaiting")) return "awaiting";
  if (states.includes("failed")) return "failed";
  const approved = states.filter((s) => s === "approved").length;
  if (approved === states.length) return "done";
  if (approved > 0) return "running"; // parcial → en curso
  return "pending";
}

export function designStages(design: PhaseNodeDesc[]): CycleStage[] {
  const byStep = new Map(design.map((p) => [p.stepId, p]));
  return STAGE_ORDER.map((key) => {
    const matched = STAGE_STEPS[key].map((id) => byStep.get(id)).filter((p): p is PhaseNodeDesc => !!p);
    const state = aggregate(matched.map((p) => p.state));
    // Fase a abrir: la primera no-aprobada del grupo (la "activa"); si todas están
    // aprobadas, la última; si no hay ninguna, sin target (línea sólo informativa).
    const active = matched.find((p) => p.state !== "approved") ?? matched[matched.length - 1];
    return { key, state, target: active ? `phase:${active.stepId}` : null };
  });
}

// El gate humano a abrir cuando el usuario clickea el ◆ de la banda: el primer gate
// AWAITING (decisión pendiente); si no hay, null (no hay nada que decidir).
export function awaitingGateTarget(model: FlowModel): string | null {
  const g = model.gates.find((g) => g.state === "awaiting");
  return g ? `phase:${g.stepId}` : null;
}

// Estado del conjunto de la banda de diseño (borde del rect STUDIO).
export function bandState(stages: CycleStage[]): StationState {
  const states = stages.map((s) => s.state);
  if (states.includes("running")) return "running";
  if (states.includes("awaiting")) return "awaiting";
  if (states.includes("failed")) return "failed";
  if (states.length > 0 && states.every((s) => s === "done")) return "done";
  if (states.includes("done")) return "running"; // parcial
  return "pending";
}

// ─────────────────────────────────────────────────────────────────────────────
// Sprint activo + contadores
// ─────────────────────────────────────────────────────────────────────────────

export interface SprintCounts {
  done: number;
  running: number;
  failed: number;
  total: number;
}

export function sprintCounts(sprint: SprintGroupDesc | null): SprintCounts {
  const c: SprintCounts = { done: 0, running: 0, failed: 0, total: 0 };
  if (!sprint) return c;
  for (const s of sprint.stories) {
    c.total++;
    if (s.status === "done") c.done++;
    else if (s.status === "running") c.running++;
    else if (s.status === "failed") c.failed++;
  }
  return c;
}

// El sprint "en el círculo": el que se está ejecutando ahora. Preferimos el sprint
// con alguna story running (el foco real); si ninguno corre, el último con progreso
// (alguna done); si nada arrancó, el primero; sin sprints, null. Determinista sobre
// el orden ya calculado por flowGraph (SP1..SPn).
export function activeSprint(sprints: SprintGroupDesc[]): SprintGroupDesc | null {
  if (sprints.length === 0) return null;
  const running = sprints.find((s) => s.stories.some((st) => st.status === "running"));
  if (running) return running;
  const withProgress = [...sprints].reverse().find((s) => s.stories.some((st) => st.status === "done"));
  if (withProgress) return withProgress;
  return sprints[0];
}

// ─────────────────────────────────────────────────────────────────────────────
// Estaciones de ceremonia (planning / review / retro) del sprint activo
// ─────────────────────────────────────────────────────────────────────────────

// Ciclo de vida de 5 estados de una estación de ceremonia como CONTROL del operador,
// derivado de datos reales (el modo del proyecto + el run/sello de la ceremonia del
// sprint activo). Cada estado tiene SU acción (la resuelve el componente):
//   off      → la ceremonia no está activada (modo "auto"): click ⇒ abrir Settings.
//   waiting  → activada pero aún no le toca (sin run): informativa ("se activa al…").
//   running  → el run está en vuelo: click ⇒ el run en vivo.
//   awaiting → el run terminó y espera tu decisión: click ⇒ el gate/run.
//   done     → sellada (con fecha): click ⇒ el run histórico.
// Nota: "awaiting" hoy no es distinguible de "running" con los datos del sprint
// (no hay estado de gate de ceremonia en el modelo); queda modelado para cuando el
// engine lo exponga. La derivación colapsa run-en-vuelo → running.
export type ControlState = "off" | "waiting" | "running" | "awaiting" | "done";

export interface CeremonyStation {
  kind: CeremonyKind;
  // Modo auto → la estación se ATENÚA (opacity), nunca se oculta: la forma no cambia.
  dim: boolean;
  control: ControlState; // ciclo de vida de 5 estados (arriba)
  state: StationState; // forma del SVG: done | running | pending
  runId: string | null; // *_run_id → abre el RunDrawer de la ceremonia
  at: number | null; // *_at (epoch) para el sello con fecha cuando control === "done"
}

// Colapsa el ciclo de vida al StationState que pinta la forma del SVG.
function shapeStateFor(control: ControlState): StationState {
  switch (control) {
    case "running":
      return "running";
    case "awaiting":
      return "awaiting";
    case "done":
      return "done";
    default:
      return "pending"; // off (atenuado) y waiting comparten la forma punteada
  }
}

function ceremonyStation(
  kind: CeremonyKind,
  sprint: SprintGroupDesc | null,
  modes: CeremonyModes,
  model: FlowModel,
): CeremonyStation {
  const dim = modes[kind] === "auto";
  const node = sprint
    ? model.ceremonies.find((c) => c.kind === kind && c.sprintId === sprint.id) ?? null
    : null;
  // Derivación del ciclo de vida:
  //   modo auto            → off (no activada).
  //   modo ceremony + run  → running (en vuelo) o done (*_at sellado).
  //   modo ceremony sin run→ waiting (activada, aún no le toca).
  let control: ControlState;
  if (dim) control = "off";
  else if (node) control = node.state === "done" ? "done" : "running";
  else control = "waiting";
  return {
    kind,
    dim,
    control,
    state: shapeStateFor(control),
    runId: node?.runId ?? null,
    at: node?.at ?? null,
  };
}

// ─────────────────────────────────────────────────────────────────────────────
// Incremento + preview
// ─────────────────────────────────────────────────────────────────────────────

export interface IncrementStation {
  state: StationState; // done si el sprint activo tiene stories done; running si hay en curso
  previewRunId: string | null; // review aceptado (reviewed_at>0) → mint de preview (#23/#28)
}

function incrementStation(sprint: SprintGroupDesc | null, model: FlowModel): IncrementStation {
  const counts = sprintCounts(sprint);
  let state: StationState = "pending";
  if (counts.running > 0) state = "running";
  else if (counts.done > 0) state = "done";
  const review = sprint
    ? model.ceremonies.find((c) => c.kind === "review" && c.sprintId === sprint.id && c.accepted) ?? null
    : null;
  return { state, previewRunId: review?.runId ?? null };
}

// Product backlog: existe (done) una vez que el diseño produjo backlog o ya hay
// ejecución; en curso mientras el backlog se genera; si no, pendiente.
export function productBacklogState(stages: CycleStage[], model: FlowModel): StationState {
  if (model.hasExecution) return "done";
  const backlog = stages.find((s) => s.key === "backlog");
  return backlog?.state ?? "pending";
}

// ─────────────────────────────────────────────────────────────────────────────
// Composición
// ─────────────────────────────────────────────────────────────────────────────

export interface CycleView {
  stages: CycleStage[];
  bandState: StationState;
  awaitingGate: string | null;
  productBacklog: StationState;
  sprint: SprintGroupDesc | null;
  counts: SprintCounts;
  planning: CeremonyStation;
  review: CeremonyStation;
  retro: CeremonyStation;
  increment: IncrementStation;
}

export function buildCycleView(model: FlowModel, modes: CeremonyModes): CycleView {
  const stages = designStages(model.designPhases);
  const sprint = activeSprint(model.sprints);
  return {
    stages,
    bandState: bandState(stages),
    awaitingGate: awaitingGateTarget(model),
    productBacklog: productBacklogState(stages, model),
    sprint,
    counts: sprintCounts(sprint),
    planning: ceremonyStation("planning", sprint, modes, model),
    review: ceremonyStation("review", sprint, modes, model),
    retro: ceremonyStation("retro", sprint, modes, model),
    increment: incrementStation(sprint, model),
  };
}
