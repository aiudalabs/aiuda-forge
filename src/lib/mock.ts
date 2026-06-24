// Datos de ejemplo — son LOS del mockup (15). Se usan cuando VIBEFORGE_API_URL no responde
// (o cuando NEXT_PUBLIC_FORCE_MOCK=1), para que la UI se pueda construir/ver sin el backend.
// El flag de modo vive en lib/api.ts.

import type {
  BoardStats,
  ControlStatus,
  Notification,
  Run,
  RunDetail,
  RunEvent,
} from "./types";

export const MOCK_PROJECT = "manitaspty";

export const mockRuns: Run[] = [
  {
    id: "run_76af79df",
    ticket: { id: "ENG-12", title: "reverse_words(s) — reordena palabras, colapsa espacios" },
    workflow: "factory-plus",
    status: "AWAITING",
    agent: "dev",
    model: "opus",
    awaitingStep: "human_gate",
    cost: 0.31,
    badges: [
      { kind: "gate", label: "gate ✓ 6 tests", tone: "ok" },
      { kind: "review", label: "review ✓ cross-model", tone: "ok" },
    ],
    project: MOCK_PROJECT,
  },
  {
    id: "run_a91c20e1",
    ticket: { id: "ENG-14", title: "Validador de cédula panameña + tests" },
    workflow: "factory-plus",
    status: "RUNNING",
    agent: "dev",
    model: "opus",
    currentStep: "implement",
    cost: 0.18,
    sandbox: "docker · egress allowlist",
    badges: [],
    dependsOn: ["ENG-1"],
    project: MOCK_PROJECT,
  },
  {
    id: "run_f7932443",
    ticket: { id: "ENG-1", title: "is_valid_email(s) + tests" },
    workflow: "factory",
    status: "DONE",
    agent: "dev",
    model: "opus",
    cost: 0.29,
    pr: { number: 3 },
    badges: [
      { kind: "gate", label: "gate ✓", tone: "ok" },
      { kind: "review", label: "review ✓", tone: "ok" },
    ],
    project: MOCK_PROJECT,
  },
  {
    id: "run_69c573fc",
    ticket: { id: "ENG-2", title: "to_roman(n) 1..3999 + tests" },
    workflow: "factory",
    status: "DONE",
    agent: "dev",
    model: "opus",
    cost: 0.34,
    pr: { number: 4 },
    badges: [
      { kind: "gate", label: "gate ✓", tone: "ok" },
      { kind: "review", label: "review ✓", tone: "ok" },
    ],
    project: MOCK_PROJECT,
  },
  {
    id: "run_e6bca38f",
    ticket: { id: "ENG-3", title: "fib(n) + tests" },
    workflow: "factory",
    status: "DONE",
    agent: "dev",
    model: "opus",
    cost: 0.3,
    pr: { number: 5 },
    dependsOn: ["ENG-1"],
    badges: [
      { kind: "gate", label: "gate ✓", tone: "ok" },
      { kind: "review", label: "review ⚠ 1 nota", tone: "warn" },
    ],
    project: MOCK_PROJECT,
  },
];

const reverseWordsDetail: RunDetail = {
  ...mockRuns[0],
  steps: [
    {
      id: "implement",
      kind: "agent",
      status: "DONE",
      agent: "dev",
      model: "opus, en docker",
      detail: "textutil.py + test_textutil.py",
      cost: 0.21,
    },
    {
      id: "gate",
      kind: "gate",
      status: "DONE",
      detail: "Ran 6 tests — OK · anti-tamper sellado",
      cost: 0.0,
    },
    {
      id: "human_gate",
      kind: "human_gate",
      status: "AWAITING",
      detail: "\"Ship to the target repo — needs human sign-off.\"",
    },
    {
      id: "pr",
      kind: "pr",
      status: "QUEUED",
      detail: "se ejecuta al aprobar",
    },
  ],
  diff: `+++ textutil.py
+def reverse_words(s):
+    return " ".join(s.split()[::-1])
+++ test_textutil.py  (6 casos)
+    def test_collapses_spaces(self): self.assertEqual(reverse_words("a   b"), "b a")
+    def test_empty(self): self.assertEqual(reverse_words(""), "")`,
  costBreakdown: {
    total: 0.31,
    byStep: [
      { step: "implement", cost: 0.21 },
      { step: "gate", cost: 0.0 },
      { step: "review", cost: 0.1 },
    ],
  },
};

const cedulaDetail: RunDetail = {
  ...mockRuns[1],
  steps: [
    {
      id: "implement",
      kind: "agent",
      status: "RUNNING",
      agent: "dev",
      model: "opus, en docker",
      detail: "cedula.py + test_cedula.py",
      cost: 0.18,
    },
    { id: "gate", kind: "gate", status: "QUEUED", detail: "unittest, --network none" },
    { id: "review", kind: "agent", status: "QUEUED", agent: "reviewer", model: "sonnet" },
    { id: "human_gate", kind: "human_gate", status: "QUEUED" },
    { id: "pr", kind: "pr", status: "QUEUED" },
  ],
  costBreakdown: { total: 0.18, byStep: [{ step: "implement", cost: 0.18 }] },
};

function doneDetail(run: Run): RunDetail {
  return {
    ...run,
    steps: [
      { id: "implement", kind: "agent", status: "DONE", agent: "dev", model: "opus", cost: run.cost * 0.7 },
      { id: "gate", kind: "gate", status: "DONE", detail: "tests OK", cost: 0 },
      { id: "review", kind: "agent", status: "DONE", agent: "reviewer", model: "sonnet", cost: run.cost * 0.3 },
      { id: "pr", kind: "pr", status: "DONE", detail: run.pr ? `PR #${run.pr.number}` : undefined },
    ],
    costBreakdown: { total: run.cost },
  };
}

export const mockRunDetails: Record<string, RunDetail> = {
  run_76af79df: reverseWordsDetail,
  run_a91c20e1: cedulaDetail,
  run_f7932443: doneDetail(mockRuns[2]),
  run_69c573fc: doneDetail(mockRuns[3]),
  run_e6bca38f: doneDetail(mockRuns[4]),
};

export const mockEvents: Record<string, RunEvent[]> = {
  run_76af79df: [
    { id: 1, runId: "run_76af79df", ts: "12:02:10", type: "run.created", message: "workflow=factory-plus" },
    { id: 2, runId: "run_76af79df", ts: "12:03:01", type: "step.status_changed", step: "implement", message: "implement RUNNING→DONE" },
    { id: 3, runId: "run_76af79df", ts: "12:03:40", type: "step.gate", step: "gate", message: "passed=true · Ran 6 tests OK" },
    { id: 4, runId: "run_76af79df", ts: "12:03:42", type: "run.awaiting_approval", step: "human_gate", message: "step=human_gate" },
  ],
  run_a91c20e1: [
    { id: 1, runId: "run_a91c20e1", ts: "12:04:02", type: "step.status_changed", step: "implement", message: "implement → RUNNING" },
    { id: 2, runId: "run_a91c20e1", ts: "12:04:21", type: "step.event", step: "implement", message: "tool_use: Write cedula.py" },
    { id: 3, runId: "run_a91c20e1", ts: "12:04:38", type: "step.event", step: "implement", message: "tool_use: Write test_cedula.py" },
    { id: 4, runId: "run_a91c20e1", ts: "12:04:53", type: "step.event", step: "implement", message: "tool_use: Bash python -m unittest -q" },
    { id: 5, runId: "run_a91c20e1", ts: "12:05:01", type: "step.event", step: "implement", message: "text: 9 tests passed; refinando casos borde…" },
  ],
};

export const mockStats: BoardStats = {
  running: 2,
  awaiting: 1,
  openPRs: 5,
  projectCost: 2.71,
};

export const mockControl: ControlStatus = { paused: false };

export const mockNotifications: Notification[] = [
  {
    id: "n1",
    runId: "run_76af79df",
    kind: "awaiting",
    title: "ENG-12 · reverse_words — espera tu visto bueno",
    ts: "12:03:42",
  },
];

// Costo total del día y tokens (topbar).
export const mockSpendToday = { cost: 3.42, tokens: "312k" };
