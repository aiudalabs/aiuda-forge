// Tests de phaseState — la derivación de estado de fase del Studio.
// Runner: Node built-in test (node:test) + type-stripping nativo (Node ≥22.18/23).
// Sin dependencias nuevas:  node --test 'src/**/*.test.ts'   (o npm run test).
//
// Foco: el transitorio "gate resuelto en DONE + fase re-corriendo" NO debe pintarse
// como aprobado (regla #18: manda la instancia MÁS RECIENTE de la fase). Cubre tanto
// `answer` (re-encola la fase con las respuestas) como `rerun` (regenera la fase) —
// ambos dejan exactamente la misma forma: designStatus RUNNING/QUEUED + gateStatus DONE.

import { test } from "node:test";
import assert from "node:assert/strict";

import { phaseState, PHASE_ICON } from "./phaseHelpers.ts";
import type { DesignPhase, DesignStepStatus } from "../../lib/types.ts";

function phase(
  designStatus: DesignStepStatus,
  gateStatus: DesignStepStatus,
  gateId: string | undefined = "prd_gate",
): DesignPhase {
  return { stepId: "prd", name: "PRD", gateId, designStatus, gateStatus };
}

test("answer transient: gate DONE + fase RUNNING → running, no approved", () => {
  const p = phase("RUNNING", "DONE");
  assert.equal(phaseState(p), "running");
  assert.notEqual(phaseState(p), "approved");
});

test("answer transient: gate DONE + fase QUEUED (re-encolada, aún sin claim) → running", () => {
  assert.equal(phaseState(phase("QUEUED", "DONE")), "running");
});

test("rerun transient: misma forma (gate DONE + fase RUNNING) → running", () => {
  // El botón Regenerar (rerun) re-corre la fase con el gate anterior aún DONE:
  // el mismo fix lo cubre. No debe seguir mostrándose como aprobado.
  assert.equal(phaseState(phase("RUNNING", "DONE")), "running");
});

test("no expone Regenerar durante el transitorio", () => {
  // PhasePanel muestra el botón "Regenerar" SOLO cuando state === "approved"
  // (`state === "approved" && phase.gateId`). Si el transitorio no reporta
  // "approved", el botón no aparece y no colisiona con la re-corrida en vuelo.
  const state = phaseState(phase("RUNNING", "DONE"));
  assert.notEqual(state, "approved");
  assert.notEqual(PHASE_ICON[state], PHASE_ICON.approved); // no es el glifo ✓
});

test("regresión: aprobación real (fase DONE + gate DONE) sigue siendo approved", () => {
  assert.equal(phaseState(phase("DONE", "DONE")), "approved");
});

test("estados normales intactos", () => {
  assert.equal(phaseState(phase("QUEUED", "QUEUED")), "pending"); // arranque
  assert.equal(phaseState(phase("RUNNING", "QUEUED")), "running"); // fase generando (1ª vez)
  assert.equal(phaseState(phase("DONE", "AWAITING")), "awaiting"); // gate esperando aprobación
  assert.equal(phaseState(phase("DONE", "FAILED")), "failed"); // gate rechazado (terminal)
  assert.equal(phaseState(phase("DONE", "QUEUED")), "running"); // gate pendiente de parquear
});

test("fase sin gate (handoff): el estado del step ES el de la fase", () => {
  // gateId vacío = fase sin gate (handoff). Ojo: pasar `undefined` dispararía el
  // valor por defecto del parámetro, así que se usa "" explícito.
  assert.equal(phaseState(phase("DONE", "QUEUED", "")), "approved");
  assert.equal(phaseState(phase("RUNNING", "QUEUED", "")), "running");
  assert.equal(phaseState(phase("FAILED", "QUEUED", "")), "failed");
});
