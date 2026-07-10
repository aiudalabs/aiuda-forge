// Test de fuga de jerga interna en la vista CICLO de /flow (flow.cycle.*).
// La vista es un tablero de control del operador: terminología 100% Scrum, CERO
// jerga interna. Este test es el criterio de aceptación de la Parte 1 — falla si
// cualquier valor de una clave flow.cycle.* (en es/en/pt) reintroduce:
//   · códigos de fase P\d (P1..P9) o de PR/issue #\d
//   · nombres de agentes internos (demo-reporter, art-director, scrum-master, Brain)
//   · nombres de archivo del registry (design.yaml, iterate.yaml)
//   · vocabulario de plomería (orchestrator, handoff, claim, groom, JIT)
// Runner: node --test 'src/**/*.test.ts'
//
// Alcance: SOLO las claves del diagrama (flow.cycle.*). Otras claves del namespace
// (p.ej. flow.band.handoff) son etiquetas legítimas fuera del tablero.

import { test } from "node:test";
import assert from "node:assert/strict";

import { flow } from "./ns/flow.ts";

const BANNED: { re: RegExp; what: string }[] = [
  { re: /\bP\d+\b/, what: "código de fase P\\d (usa el nombre Scrum/humano)" },
  { re: /#\d+/, what: "referencia #\\d de PR/issue" },
  { re: /\b(demo-reporter|art-director|scrum-master|Brain)\b/i, what: "nombre de agente interno" },
  { re: /\b[\w-]+\.ya?ml\b/i, what: "nombre de archivo .yaml del registry" },
  { re: /\borchestrator\b/i, what: "jerga: orchestrator" },
  { re: /\bhandoff\b/i, what: "jerga: handoff" },
  { re: /\bclaim\b/i, what: "jerga: claim" },
  { re: /\bgroom\b/i, what: "jerga: groom" },
  { re: /\bJIT\b/, what: "jerga: JIT" },
];

for (const lang of ["es", "en", "pt"] as const) {
  test(`flow.cycle.* sin jerga interna (${lang})`, () => {
    const dict = flow[lang] as Record<string, string>;
    const cycleKeys = Object.keys(dict).filter((k) => k.startsWith("flow.cycle."));
    assert.ok(cycleKeys.length > 20, "deberían existir las claves del diagrama del ciclo");
    for (const key of cycleKeys) {
      const val = dict[key];
      for (const { re, what } of BANNED) {
        assert.ok(!re.test(val), `${lang} · ${key} = «${val}» reintroduce ${what}`);
      }
    }
  });
}
