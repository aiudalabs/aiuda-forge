// Extracción de señales legibles de la salida cruda de un paso del run.
// Los `detail` que vienen del kernel son texto largo (markdown del agente, salida
// de tests, veredictos del review…). Para que el drawer sea escaneable derivamos
// una línea-resumen y un tono por paso: "9 tests passed", "VERDICT: works",
// "PR #4 opened". El detalle completo sigue disponible (colapsable) — esto es solo
// el titular.

import type { RunStep } from "./types";

export type SignalTone = "ok" | "warn" | "info";

export interface StepSignal {
  summary: string; // titular de una línea ("9 tests passed")
  tone: SignalTone;
  pr?: { number: number; url?: string }; // si el paso abrió/menciona un PR
}

// `Ran 9 tests in 0.01s` … `OK` / `FAILED (failures=2)` — salida de unittest.
const RAN_TESTS = /Ran\s+(\d+)\s+tests?/i;
const UNITTEST_OK = /\bOK\b/;
const UNITTEST_FAILED = /FAILED\s*\((?:failures|errors)=(\d+)/i;

// pytest: `5 passed`, `2 failed`, `1 error` en la línea de resumen.
const PYTEST_PASSED = /(\d+)\s+passed/i;
const PYTEST_FAILED = /(\d+)\s+(?:failed|error)/i;

// review: `VERDICT: works` | `VERDICT: broken` (también acepta "veredicto").
const VERDICT = /\b(?:VERDICT|VEREDICTO)\s*[:=]\s*(works|broken|funciona|roto|pass|fail)/i;

// PR: número (#4) y URL de GitHub si está presente.
const PR_URL = /https?:\/\/github\.com\/[^\s)"']+\/pull\/(\d+)/i;
const PR_HASH = /\bPR\s*#?(\d+)/i;

function extractPr(text: string): { number: number; url?: string } | undefined {
  const urlMatch = text.match(PR_URL);
  if (urlMatch) return { number: Number(urlMatch[1]), url: urlMatch[0] };
  const hashMatch = text.match(PR_HASH);
  if (hashMatch) return { number: Number(hashMatch[1]) };
  return undefined;
}

// Cuenta de tests a partir de salida de unittest o pytest, si la hay.
function testSignal(text: string): StepSignal | undefined {
  const failedUnit = text.match(UNITTEST_FAILED);
  const ran = text.match(RAN_TESTS);
  if (ran) {
    const n = Number(ran[1]);
    if (failedUnit) {
      return { summary: `${failedUnit[1]} of ${n} tests failed`, tone: "warn" };
    }
    if (UNITTEST_OK.test(text)) {
      return { summary: `${n} ${n === 1 ? "test" : "tests"} passed`, tone: "ok" };
    }
    return { summary: `${n} ${n === 1 ? "test" : "tests"} run`, tone: "info" };
  }

  const pyFailed = text.match(PYTEST_FAILED);
  const pyPassed = text.match(PYTEST_PASSED);
  if (pyFailed) {
    return { summary: `${pyFailed[1]} failed`, tone: "warn" };
  }
  if (pyPassed) {
    return { summary: `${pyPassed[1]} passed`, tone: "ok" };
  }
  return undefined;
}

function verdictSignal(text: string): StepSignal | undefined {
  const m = text.match(VERDICT);
  if (!m) return undefined;
  const raw = m[1].toLowerCase();
  const broken = raw === "broken" || raw === "roto" || raw === "fail";
  return {
    summary: `VERDICT: ${broken ? "broken" : "works"}`,
    tone: broken ? "warn" : "ok",
  };
}

// Primera línea no vacía del detalle, recortada — fallback cuando no hay señal
// estructurada (p. ej. el paso `implement` que es markdown libre).
function firstLine(text: string, max = 96): string {
  const line = text
    .split("\n")
    .map((l) => l.replace(/^#+\s*/, "").trim()) // ignora marcadores de heading
    .find((l) => l.length > 0);
  if (!line) return "";
  return line.length > max ? line.slice(0, max - 1) + "…" : line;
}

// Deriva el titular de un paso. El `kind`/`id` orienta qué señal priorizar, pero
// caemos a heurísticas de texto si el detalle no encaja (los `detail` no son
// estrictos). Devuelve undefined si no hay nada que mostrar.
export function stepSignal(step: RunStep): StepSignal | undefined {
  const text = step.detail?.trim();
  if (!text) return undefined;

  const isGate = step.kind === "gate" || step.id === "gate";
  const isReview =
    step.kind === "agentic_verify" || step.id === "review" || /review|verif/i.test(step.id);
  const isPr = step.kind === "pr" || step.id === "pr";

  const pr = extractPr(text);

  // pr: el titular es el PR; el link va aparte para hacerlo clicable.
  if (isPr && pr) {
    return { summary: `PR #${pr.number} opened`, tone: "ok", pr };
  }

  // review: veredicto manda; si no hay, intenta tests; si no, primera línea.
  if (isReview) {
    const v = verdictSignal(text);
    if (v) return { ...v, pr };
  }

  // gate: cuenta de tests manda.
  if (isGate) {
    const t = testSignal(text);
    if (t) return { ...t, pr };
  }

  // Heurística libre para cualquier paso: prueba veredicto → tests → PR → 1ª línea.
  const v = verdictSignal(text);
  if (v) return { ...v, pr };
  const t = testSignal(text);
  if (t) return { ...t, pr };
  if (pr) return { summary: `PR #${pr.number}`, tone: "info", pr };

  const fl = firstLine(text);
  return fl ? { summary: fl, tone: "info", pr } : undefined;
}

// ¿Vale la pena colapsar? Detalles cortos de una línea se muestran inline; los
// largos o multilínea se colapsan detrás del titular.
export function isLongDetail(detail?: string): boolean {
  if (!detail) return false;
  const t = detail.trim();
  return t.includes("\n") || t.length > 120;
}
