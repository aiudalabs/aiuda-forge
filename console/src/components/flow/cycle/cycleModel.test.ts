// Tests de la derivación PURA de la vista Ciclo (cycleModel.ts).
// Runner: node:test + type-stripping nativo (Node ≥22.18/23), sin deps nuevas:
//   node --test 'src/**/*.test.ts'   (o npm run test)
//
// Foco (criterio de aceptación): estación→estado como funciones puras — agregación
// de las 6 líneas de diseño (prd cubre prd+data_model, etc.), pick del sprint activo
// + contadores, ceremonias atenuadas en auto vs con sello en ceremony, gate awaiting,
// e incremento con preview del review aceptado. Reusa el MISMO FlowModel del grafo.

import { test } from "node:test";
import assert from "node:assert/strict";

import { buildFlow, type CeremonyModes } from "../flowGraph.ts";
import { phaseState } from "../../studio/phaseHelpers.ts";
import {
  buildCycleView,
  designStages,
  activeSprint,
  sprintCounts,
  awaitingGateTarget,
  productBacklogState,
} from "./cycleModel.ts";
import type { DesignPhase, OrchestratorTicket, Sprint } from "../../../lib/types.ts";

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

function model(opts: {
  phases?: DesignPhase[];
  sprints?: Sprint[];
  tickets?: OrchestratorTicket[];
  modes?: CeremonyModes;
}) {
  return buildFlow({
    phases: opts.phases ?? [],
    sprints: opts.sprints ?? [],
    tickets: opts.tickets ?? [],
    modes: opts.modes ?? AUTO,
    stateOf: phaseState,
  });
}

// ── Agregación de las 6 líneas de diseño ────────────────────────────────────────

test("designStages: 6 líneas fijas en orden, prd agrega prd+data_model", () => {
  const m = model({
    phases: [
      phase("discovery", "DONE", "DONE"),
      phase("constitution", "DONE", "DONE"),
      phase("prd", "DONE", "DONE"),
      phase("data_model", "RUNNING", "QUEUED"),
      phase("architecture", "QUEUED", "QUEUED"),
    ],
  });
  const stages = designStages(m.designPhases);
  assert.deepEqual(
    stages.map((s) => s.key),
    ["brief", "constitution", "prd", "architecture", "ui", "backlog"],
  );
  assert.equal(stages[0].state, "done"); // discovery approved
  // prd agrega prd(approved)+data_model(running) → running gana
  assert.equal(stages[2].state, "running");
  assert.equal(stages[3].state, "pending"); // architecture queued sin gate resuelto
  assert.equal(stages[4].state, "pending"); // ui ausente → pending
});

test("designStages: awaiting se propaga y el target apunta a la fase activa", () => {
  const m = model({
    phases: [phase("discovery", "DONE", "DONE"), phase("prd", "DONE", "AWAITING")],
  });
  const stages = designStages(m.designPhases);
  const prd = stages.find((s) => s.key === "prd")!;
  assert.equal(prd.state, "awaiting");
  assert.equal(prd.target, "phase:prd"); // la primera no-aprobada del grupo
  const brief = stages.find((s) => s.key === "brief")!;
  assert.equal(brief.state, "done");
  assert.equal(brief.target, "phase:discovery"); // todas aprobadas → última (única)
});

test("designStages: todo aprobado → done; nada → pending sin target", () => {
  const done = designStages(model({ phases: [phase("ui", "DONE", "DONE"), phase("mockups", "DONE", "DONE")] }).designPhases);
  assert.equal(done.find((s) => s.key === "ui")!.state, "done");
  const empty = designStages(model({}).designPhases);
  assert.equal(empty.find((s) => s.key === "brief")!.state, "pending");
  assert.equal(empty.find((s) => s.key === "brief")!.target, null);
});

// ── Gate awaiting ───────────────────────────────────────────────────────────────

test("awaitingGateTarget: primer gate esperando al humano, o null", () => {
  const m = model({ phases: [phase("discovery", "DONE", "DONE"), phase("prd", "DONE", "AWAITING")] });
  assert.equal(awaitingGateTarget(m), "phase:prd");
  const none = model({ phases: [phase("discovery", "DONE", "DONE")] });
  assert.equal(awaitingGateTarget(none), null);
});

// ── Sprint activo + contadores ──────────────────────────────────────────────────

test("activeSprint: prioriza el sprint con story running", () => {
  const m = model({
    sprints: [sprint("SP1"), sprint("SP2"), sprint("SP3")],
    tickets: [
      story("a", "SP1", { status: "done" }),
      story("b", "SP2", { status: "running" }),
      story("c", "SP3", { status: "backlog" }),
    ],
  });
  assert.equal(activeSprint(m.sprints)?.id, "SP2");
});

test("activeSprint: sin running → último con progreso; sin progreso → primero", () => {
  const withDone = model({
    sprints: [sprint("SP1"), sprint("SP2")],
    tickets: [story("a", "SP1", { status: "done" }), story("b", "SP2", { status: "backlog" })],
  });
  assert.equal(activeSprint(withDone.sprints)?.id, "SP1");
  const fresh = model({
    sprints: [sprint("SP1"), sprint("SP2")],
    tickets: [story("a", "SP1", { status: "backlog" }), story("b", "SP2", { status: "backlog" })],
  });
  assert.equal(activeSprint(fresh.sprints)?.id, "SP1");
  assert.equal(activeSprint([]), null);
});

test("sprintCounts: cuenta done/running/failed/total del sprint", () => {
  const m = model({
    sprints: [sprint("SP1")],
    tickets: [
      story("a", "SP1", { status: "done" }),
      story("b", "SP1", { status: "done" }),
      story("c", "SP1", { status: "running" }),
      story("d", "SP1", { status: "failed" }),
      story("e", "SP1", { status: "backlog" }),
    ],
  });
  assert.deepEqual(sprintCounts(activeSprint(m.sprints)), { done: 2, running: 1, failed: 1, total: 5 });
  assert.deepEqual(sprintCounts(null), { done: 0, running: 0, failed: 0, total: 0 });
});

// ── Ceremonias: atenuado en auto, sello en ceremony ─────────────────────────────

test("ceremony: modo auto atenúa (dim) pero conserva la estación", () => {
  const m = model({
    sprints: [sprint("SP1")],
    tickets: [story("a", "SP1", { status: "running" })],
    modes: AUTO,
  });
  const v = buildCycleView(m, AUTO);
  assert.equal(v.planning.dim, true);
  assert.equal(v.review.dim, true);
  assert.equal(v.retro.dim, true);
  assert.equal(v.planning.state, "pending"); // sin run
});

test("ceremony: en modo ceremony con run → estado vivo + runId para el drawer", () => {
  const m = model({
    sprints: [
      sprint("SP1", {
        planning_run_id: "run-plan",
        planned_at: 111,
        review_run_id: "run-rev",
        reviewed_at: 222,
      }),
    ],
    tickets: [story("a", "SP1", { status: "done" })],
    modes: CEREMONY,
  });
  const v = buildCycleView(m, CEREMONY);
  assert.equal(v.planning.dim, false);
  assert.equal(v.planning.state, "done");
  assert.equal(v.planning.runId, "run-plan");
  assert.equal(v.retro.state, "pending"); // sin retro_run_id
  assert.equal(v.retro.runId, null);
});

// ── Incremento + preview del review aceptado ────────────────────────────────────

test("increment: preview sólo cuando el review fue aceptado (reviewed_at>0)", () => {
  const accepted = model({
    sprints: [sprint("SP1", { review_run_id: "run-rev", reviewed_at: 999 })],
    tickets: [story("a", "SP1", { status: "done" })],
  });
  const v = buildCycleView(accepted, CEREMONY);
  assert.equal(v.increment.state, "done"); // hay story done
  assert.equal(v.increment.previewRunId, "run-rev");

  const notAccepted = model({
    sprints: [sprint("SP1", { review_run_id: "run-rev" })], // sin reviewed_at
    tickets: [story("a", "SP1", { status: "running" })],
  });
  const v2 = buildCycleView(notAccepted, CEREMONY);
  assert.equal(v2.increment.state, "running");
  assert.equal(v2.increment.previewRunId, null);
});

// ── Product backlog ─────────────────────────────────────────────────────────────

test("productBacklogState: done si hay ejecución; si no, sigue la línea backlog", () => {
  const exec = model({
    phases: [phase("backlog", "QUEUED", "QUEUED")],
    sprints: [sprint("SP1")],
    tickets: [story("a", "SP1", { status: "backlog" })],
  });
  assert.equal(productBacklogState(designStages(exec.designPhases), exec), "done");
  const designed = model({ phases: [phase("backlog", "DONE", "DONE")] });
  assert.equal(productBacklogState(designStages(designed.designPhases), designed), "done");
  const fresh = model({ phases: [phase("backlog", "QUEUED", "QUEUED")] });
  assert.equal(productBacklogState(designStages(fresh.designPhases), fresh), "pending");
});
