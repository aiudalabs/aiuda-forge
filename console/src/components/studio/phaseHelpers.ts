// Helpers de fase compartidos entre DesignPipeline y PhasePanel — estado derivado
// del step/gate, labels i18n, orden de fase activa. Sin JSX: puro TS.

import type { DesignPhase } from "@/lib/types";

// Estado compuesto de una fase: derivado del estado del paso de diseño y del gate.
export type PhaseState = "pending" | "running" | "awaiting" | "approved" | "failed";

// Nombre de fase i18n: usa la clave studio.phase.<stepId> si existe; si no cae al
// label del workflow (para fases custom de un flujo definido por el usuario).
export function phaseLabel(t: (k: string) => string, stepId: string, fallback: string): string {
  const k = "studio.phase." + stepId;
  const v = t(k);
  return v === k ? fallback : v;
}

export function phaseState(p: DesignPhase): PhaseState {
  // Fase sin gate (handoff): el estado del step ES el estado de la fase; si no,
  // un run 100% terminado quedaría clavado en "6/7 · running" para siempre.
  if (!p.gateId) {
    if (p.designStatus === "DONE") return "approved";
    if (p.designStatus === "FAILED") return "failed";
    if (p.designStatus === "RUNNING") return "running";
    return "pending";
  }
  // La instancia MÁS RECIENTE de la fase manda (misma regla que el bug #18 de
  // steps duplicados: al haber varias tasks de un step, gana la más nueva). Cuando
  // la fase vuelve a correr —`answer` re-encola la fase con las respuestas, `rerun`
  // la regenera— queda una task nueva RUNNING/QUEUED de la fase junto al gate ANTERIOR
  // aún resuelto en DONE. Ese gate DONE es viejo: la fase está EN CURSO, no aprobada.
  // Chequear la fase en curso ANTES del gate DONE evita pintar ✓ verde, adelantar la
  // navegación (activePhaseIndex) y exponer "Regenerar" durante la re-corrida.
  if (p.gateStatus === "DONE" && (p.designStatus === "RUNNING" || p.designStatus === "QUEUED")) {
    return "running";
  }
  if (p.gateStatus === "DONE") return "approved";
  if (p.gateStatus === "FAILED") return "failed";
  if (p.gateStatus === "AWAITING") return "awaiting";
  // The phase STEP itself died (agent error / timeout): no gate was ever created, so
  // gateStatus stays QUEUED — surface it as failed instead of falling through to pending.
  if (p.designStatus === "FAILED") return "failed";
  if (p.designStatus === "RUNNING") return "running";
  if (p.designStatus === "DONE" && p.gateStatus === "QUEUED") return "running"; // gate pendiente
  return "pending";
}

export const PHASE_ICON: Record<PhaseState, string> = {
  pending: "◌",
  running: "●",
  awaiting: "⏸",
  approved: "✓",
  failed: "✕",
};

// Mapeo de estado a clase CSS del glifo en el stepper horizontal.
export const PHASE_GLYPH_CLS: Record<PhaseState, string> = {
  pending: "ph-pend",
  running: "ph-run",
  awaiting: "ph-await",
  approved: "ph-ok",
  failed: "ph-fail",
};


// Fase activa: la primera que no está aprobada.
export function activePhaseIndex(phases: DesignPhase[]): number {
  const idx = phases.findIndex((p) => phaseState(p) !== "approved");
  return idx === -1 ? phases.length - 1 : idx;
}

// Pasos cuyo artefacto es un backlog.yaml (se renderiza con tarjetas de historia).
// "plan" es el paso del workflow "iterate": escribe docs/backlog.yaml (el backlog
// DELTA) igual que "backlog" en el diseño completo, así que se renderiza igual.
export const BACKLOG_STEPS = new Set(["backlog", "plan"]);
