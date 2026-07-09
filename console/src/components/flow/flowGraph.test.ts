// Tests de la derivación pura de la vista Flow (flowGraph.ts).
// Runner: node:test + type-stripping nativo (Node ≥22.18/23), sin deps nuevas:
//   node --test 'src/**/*.test.ts'   (o npm run test)
//
// Foco (criterio de aceptación de la tarea): bandas diseño/handoff, sellos por modo
// (auto no pinta, ceremony sí), ceremonias por feature-detect (*_run_id), answered
// ≠ approved (fix #22 reusado), y última instancia ante steps duplicados (#18).

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  buildFlow,
  buildPhaseNodes,
  buildSprintGroups,
  buildCeremonyNodes,
  buildStoryEdges,
  phasesFromSteps,
  latestInstanceStatus,
  sprintSeals,
  sprintOrder,
  type CeremonyModes,
  type PhaseDef,
  type RawStep,
} from "./flowGraph.ts";
// El phaseState REAL de phaseHelpers, inyectado en la derivación (se reusa, no se
// reimplementa). Importado relativo + .ts: la convención de los tests del repo.
import { phaseState } from "../studio/phaseHelpers.ts";
import type { DesignPhase, OrchestratorTicket, Sprint } from "../../lib/types.ts";

const AUTO: CeremonyModes = { planning: "auto", review: "auto", retro: "auto" };
const CEREMONY: CeremonyModes = { planning: "ceremony", review: "ceremony", retro: "ceremony" };

function phase(
  stepId: string,
  designStatus: DesignPhase["designStatus"],
  gateStatus: DesignPhase["gateStatus"],
  gateId: string | undefined = `${stepId}_gate`,
): DesignPhase {
  return { stepId, name: stepId.toUpperCase(), gateId, designStatus, gateStatus };
}

function sprint(id: string, extra: Partial<Sprint> = {}): Sprint {
  return { id, name: `Sprint ${id}`, goal: "goal", ...extra };
}

function story(id: string, sprintId: string, extra: Partial<OrchestratorTicket> = {}): OrchestratorTicket {
  return { id, title: id, status: "backlog", deps: [], sprint_id: sprintId, ...extra };
}

// ── Bandas ────────────────────────────────────────────────────────────────────

test("bandas: fases de diseño vs handoff (docs_pr/handoff a HANDOFF)", () => {
  const { design, handoff } = buildPhaseNodes(
    [
      phase("prd", "DONE", "DONE"),
      phase("architecture", "DONE", "DONE"),
      phase("docs_pr", "DONE", "QUEUED", ""),
      phase("handoff", "DONE", "QUEUED", ""),
    ],
    phaseState,
  );
  assert.deepEqual(
    design.map((n) => n.stepId),
    ["prd", "architecture"],
  );
  assert.deepEqual(
    handoff.map((n) => n.stepId),
    ["docs_pr", "handoff"],
  );
  assert.equal(design.every((n) => n.band === "design"), true);
  assert.equal(handoff.every((n) => n.band === "handoff"), true);
});

// ── answered ≠ approved (fix #22, reusado vía phaseState) ───────────────────────

test("answered: gate DONE + fase re-corriendo → gate node running, NO approved", () => {
  const { gates } = buildPhaseNodes([phase("prd", "RUNNING", "DONE")], phaseState);
  assert.equal(gates.length, 1);
  assert.equal(gates[0].state, "running");
  assert.notEqual(gates[0].state, "approved");
});

test("aprobación real (fase DONE + gate DONE) → gate node approved", () => {
  const { gates } = buildPhaseNodes([phase("prd", "DONE", "DONE")], phaseState);
  assert.equal(gates[0].state, "approved");
});

test("gate esperando decisión (gate AWAITING) → awaiting", () => {
  const { gates, design } = buildPhaseNodes([phase("prd", "DONE", "AWAITING")], phaseState);
  assert.equal(gates[0].state, "awaiting");
  assert.equal(design[0].state, "awaiting");
});

test("fase sin gate (handoff) no genera nodo gate", () => {
  const { gates } = buildPhaseNodes([phase("handoff", "DONE", "QUEUED", "")], phaseState);
  assert.equal(gates.length, 0);
});

// ── Última instancia ante steps duplicados (#18): steps → nodo ──────────────────

test("steps duplicados por retry: la última instancia (DONE) gana en el nodo", () => {
  const defs: PhaseDef[] = [{ stepId: "prd", name: "PRD", gateId: "prd_gate" }];
  // created_at ASC: primero FAILED (intento viejo), luego DONE (retry).
  const steps: RawStep[] = [
    { step_id: "prd", status: "FAILED" },
    { step_id: "prd", status: "DONE" },
    { step_id: "prd_gate", status: "AWAITING" },
  ];
  assert.equal(latestInstanceStatus(steps, "prd"), "DONE");
  const phases = phasesFromSteps(defs, steps);
  const { design } = buildPhaseNodes(phases, phaseState);
  // designStatus DONE + gate AWAITING → la fase espera decisión, NO failed.
  assert.equal(design[0].state, "awaiting");
});

test("latestInstanceStatus: sin coincidencias → QUEUED", () => {
  assert.equal(latestInstanceStatus([{ step_id: "x", status: "DONE" }], "prd"), "QUEUED");
});

// ── Sellos por modo ─────────────────────────────────────────────────────────────

test("modo auto: sin sellos (no se renderiza ninguno)", () => {
  const s = sprint("SP1", { planned_at: 123, reviewed_at: 456, retro_at: 789 });
  assert.deepEqual(sprintSeals(s, AUTO), {});
});

test("modo ceremony: sellos done/running/pending derivados de *_at y *_run_id", () => {
  const s = sprint("SP1", {
    planned_at: 123, // planning aplicado → done
    review_run_id: "run_rev", // review en vuelo (sin reviewed_at) → running
    // retro sin nada → pending
  });
  assert.deepEqual(sprintSeals(s, CEREMONY), {
    planning: "done",
    review: "running",
    retro: "pending",
  });
});

test("modo mixto: solo el modo en ceremony pinta su sello", () => {
  const s = sprint("SP1", { planned_at: 1 });
  const modes: CeremonyModes = { planning: "ceremony", review: "auto", retro: "auto" };
  assert.deepEqual(sprintSeals(s, modes), { planning: "done" });
});

// ── Ceremonias por feature-detect (*_run_id) ────────────────────────────────────

test("ceremonias: solo existen cuando su *_run_id existe (nada hardcodeado)", () => {
  const sprints = [
    sprint("SP1", { review_run_id: "r_rev", reviewed_at: 999, retro_run_id: "r_retro" }),
    sprint("SP2", { planning_run_id: "r_plan" }), // planning en vuelo (sin planned_at)
    sprint("SP3"), // sin ninguna ceremonia
  ];
  const cer = buildCeremonyNodes(sprints);
  const ids = cer.map((c) => c.id).sort();
  assert.deepEqual(ids, [
    "ceremony:planning:SP2",
    "ceremony:retro:SP1",
    "ceremony:review:SP1",
  ]);
  const planning = cer.find((c) => c.id === "ceremony:planning:SP2")!;
  assert.equal(planning.state, "running"); // run existe, planned_at 0
  const review = cer.find((c) => c.id === "ceremony:review:SP1")!;
  assert.equal(review.state, "done");
  assert.equal(review.accepted, true); // reviewed_at>0 → preview disponible (#23)
});

test("review sin aceptar (sin reviewed_at) → accepted false, sin preview", () => {
  const cer = buildCeremonyNodes([sprint("SP1", { review_run_id: "r" })]);
  assert.equal(cer[0].accepted, false);
});

// ── Grupos de sprint + orden numérico ───────────────────────────────────────────

test("sprintOrder: número final; SP2 < SP10", () => {
  assert.equal(sprintOrder("SP2"), 2);
  assert.equal(sprintOrder("SP10"), 10);
  assert.equal(sprintOrder("nope"), Number.POSITIVE_INFINITY);
});

test("grupos: ordenados numéricamente, stories agrupadas, kind bug|story", () => {
  const sprints = [sprint("SP10"), sprint("SP2")];
  const tickets = [
    story("s1", "SP2", { kind: "bug" }),
    story("s2", "SP2"),
    story("s3", "SP10"),
  ];
  const groups = buildSprintGroups(sprints, tickets, AUTO);
  assert.deepEqual(groups.map((g) => g.id), ["SP2", "SP10"]);
  assert.equal(groups[0].stories.length, 2);
  assert.equal(groups[0].stories.find((s) => s.ticketId === "s1")!.kind, "bug");
  assert.equal(groups[0].stories.find((s) => s.ticketId === "s2")!.kind, "story");
});

test("grupos: un sprint referenciado por stories pero ausente del /sprints igual aparece", () => {
  const groups = buildSprintGroups([], [story("s1", "SP7")], AUTO);
  assert.deepEqual(groups.map((g) => g.id), ["SP7"]);
  assert.equal(groups[0].name, "SP7"); // fallback al id
});

// ── Aristas de dependencia (cross-sprint punteada, dep colgante omitida) ─────────

test("aristas: cross-sprint marcada, misma-sprint no, dep colgante omitida", () => {
  const tickets = [
    story("a", "SP1", { status: "done" }),
    story("b", "SP1", { deps: ["a"] }), // misma sprint
    story("c", "SP2", { deps: ["a", "ghost"] }), // cross-sprint + colgante
  ];
  const edges = buildStoryEdges(tickets);
  assert.equal(edges.length, 2); // ghost omitida
  const ab = edges.find((e) => e.id === "a->b")!;
  assert.equal(ab.crossSprint, false);
  assert.equal(ab.source, "story:a");
  assert.equal(ab.target, "story:b");
  const ac = edges.find((e) => e.id === "a->c")!;
  assert.equal(ac.crossSprint, true);
  assert.equal(ac.depStatus, "done");
});

// ── Composición end-to-end + flags de banda ─────────────────────────────────────

test("buildFlow: hasDesign/hasExecution y ensamblado completo", () => {
  const model = buildFlow({
    phases: [phase("prd", "DONE", "DONE"), phase("handoff", "DONE", "QUEUED", "")],
    sprints: [sprint("SP1", { planning_run_id: "r_plan", planned_at: 5 })],
    tickets: [story("s1", "SP1"), story("s2", "SP1", { deps: ["s1"] })],
    modes: CEREMONY,
    stateOf: phaseState,
  });
  assert.equal(model.hasDesign, true);
  assert.equal(model.hasExecution, true);
  assert.equal(model.designPhases.length, 1);
  assert.equal(model.handoffPhases.length, 1);
  assert.equal(model.sprints.length, 1);
  assert.equal(model.sprints[0].seals.planning, "done");
  assert.equal(model.ceremonies.length, 1);
  assert.equal(model.storyEdges.length, 1);
});

test("buildFlow: proyecto vacío (sin fases ni stories) → flags en false", () => {
  const model = buildFlow({ phases: [], sprints: [], tickets: [], modes: AUTO, stateOf: phaseState });
  assert.equal(model.hasDesign, false);
  assert.equal(model.hasExecution, false);
});
