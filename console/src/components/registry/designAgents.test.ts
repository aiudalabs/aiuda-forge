// Tests del filtro design/ejecución del Registry.
// Runner: Node built-in test (node:test) + type-stripping nativo.  node --test 'src/**/*.test.ts'
//
// El bug original: un allowlist estático (analyst/pm/architect/designer/ux-designer/
// scrum-master/po/product-owner) escondía agentes de ceremonia REALES y listaba ids que no
// existen. Este test fija el contrato contra el set REAL de engine/registry/agents/.

import { test } from "node:test";
import assert from "node:assert/strict";

import { isExecutionLane, designAgentIds } from "./designAgents.ts";

// El set real de ids de engine/registry/agents/ (2026-07-09).
const REGISTRY_AGENTS = [
  "analyst", "architect", "art-director", "data-modeler", "demo-reporter", "designer",
  "dev", "firebase-dev", "flutter-dev", "iteration-planner", "planner", "pm", "principles",
  "product-advisor", "python-dev", "react-dev", "retro-analyst", "reviewer", "scrum-master",
  "story-detailer", "ux", "verifier",
];

test("execution lanes are hidden: dev, reviewer, verifier y la familia *-dev", () => {
  for (const id of ["dev", "reviewer", "verifier", "python-dev", "react-dev", "flutter-dev", "firebase-dev"]) {
    assert.equal(isExecutionLane(id), true, `${id} debería ocultarse (lane de ejecución)`);
  }
});

test("los agentes de ceremonia/diseño que el bug escondía ahora se muestran", () => {
  // Exactamente los que el reporte pidió recuperar + el resto del método.
  for (const id of ["planner", "demo-reporter", "retro-analyst", "art-director", "principles",
                    "data-modeler", "story-detailer", "product-advisor", "iteration-planner",
                    "analyst", "pm", "architect", "designer", "ux", "scrum-master"]) {
    assert.equal(isExecutionLane(id), false, `${id} NO es lane de ejecución — debería mostrarse`);
  }
});

test("designAgentIds sobre el registry real: muestra el método, oculta 7 lanes de código", () => {
  const shown = designAgentIds(REGISTRY_AGENTS);
  // Ninguno de los 7 lanes de ejecución sobrevive.
  for (const hidden of ["dev", "reviewer", "verifier", "python-dev", "react-dev", "flutter-dev", "firebase-dev"]) {
    assert.equal(shown.includes(hidden), false, `${hidden} no debería estar en la lista mostrada`);
  }
  // Los que el bug escondía SÍ sobreviven.
  for (const want of ["planner", "demo-reporter", "retro-analyst", "art-director", "principles", "data-modeler"]) {
    assert.equal(shown.includes(want), true, `${want} debería estar en la lista mostrada`);
  }
  // 22 agentes − 7 lanes de ejecución = 15 de método.
  assert.equal(shown.length, 15);
});

test("un stack lane futuro (supabase-dev, go-dev) se oculta solo, sin tocar la lista", () => {
  assert.equal(isExecutionLane("supabase-dev"), true);
  assert.equal(isExecutionLane("go-dev"), true);
});

test("no hay falsos positivos: ids que contienen 'dev' pero no son lanes", () => {
  // Ninguno del método real termina en -dev ni es dev/reviewer/verifier.
  assert.equal(isExecutionLane("data-modeler"), false);
  assert.equal(isExecutionLane("demo-reporter"), false);
});
