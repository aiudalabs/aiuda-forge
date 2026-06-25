// Datos de ejemplo — son LOS del mockup (15). Se usan cuando VIBEFORGE_API_URL no responde
// (o cuando NEXT_PUBLIC_FORCE_MOCK=1), para que la UI se pueda construir/ver sin el backend.
// El flag de modo vive en lib/api.ts.

import type {
  BoardStats,
  ControlStatus,
  DesignRun,
  Epic,
  MetricsPayload,
  Notification,
  OrchestratorTicket,
  Project,
  Run,
  RunDetail,
  RunEvent,
  SettingsPayload,
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

// ── Registry mock ─────────────────────────────────────────────────────────────

export const mockRegistryIds: Record<string, string[]> = {
  agents: ["dev", "reviewer", "verifier"],
  skills: ["coding-conventions", "security-checklist", "adversarial-review"],
  workflows: ["factory", "factory-plus", "gated"],
};

// Raw YAML / markdown content per item. Serves as fallback in mock mode.
export const mockRegistryContent: Record<string, Record<string, string>> = {
  agents: {
    dev: `id: dev\nversion: 1.0.0\nmodel: claude-opus-4-8\neffort: high\nrole: |\n  Implementa el ticket dejando el árbol modificado, sin commit.\nskills:\n  - coding-conventions\ntools:\n  - read\n  - edit\n  - write\n  - bash\ninputs:\n  - ticket\noutputs:\n  - diff\n`,
    reviewer: `id: reviewer\nversion: 1.0.0\nmodel: claude-sonnet-4-6\neffort: high\nrole: |\n  Revisa adversarialmente — busca el bug que el dev no vio.\nskills:\n  - security-checklist\ntools:\n  - read\n  - bash\ninputs:\n  - ticket\n  - diff_hint\noutputs:\n  - verdict\n  - notes\n`,
    verifier: `id: verifier\nversion: 1.0.0\nmodel: claude-sonnet-4-6\neffort: medium\nrole: |\n  Verificador fresco: maneja la app/tests y juzga works|broken con evidencia.\nskills: []\ntools:\n  - read\n  - bash\non_fail: implement\ninputs:\n  - ticket\noutputs:\n  - verdict\n`,
  },
  skills: {
    "coding-conventions": `# coding-conventions\n\nEsta skill define las convenciones de código del proyecto.\n\n## Reglas\n- Nombres en snake_case para Python, camelCase para JS.\n- Máximo 2 niveles de anidamiento.\n- Early returns preferidos.\n`,
    "security-checklist": `# security-checklist\n\nChecklist de seguridad aplicado en cada revisión.\n\n## Items\n- No secrets en el código.\n- Validar entradas de usuario.\n- SQL parametrizado.\n`,
    "adversarial-review": `# adversarial-review\n\nGuía de revisión adversarial para encontrar bugs no obvios.\n`,
  },
  workflows: {
    factory: `id: factory\nsteps:\n  - id: implement\n    kind: agent\n    agent: dev\n  - id: gate\n    kind: gate\n    on_fail: implement\n  - id: review\n    kind: agent\n    agent: reviewer\n  - id: pr\n    kind: pr\n`,
    "factory-plus": `id: factory-plus\nsteps:\n  - id: implement\n    kind: agent\n    agent: dev\n  - id: gate\n    kind: gate\n    on_fail: implement\n  - id: review\n    kind: agent\n    agent: reviewer\n  - id: verify\n    kind: agentic_verify\n    agent: verifier\n    on_fail: implement\n  - id: human_gate\n    kind: human_gate\n  - id: pr\n    kind: pr\n`,
    gated: `id: gated\nsteps:\n  - id: implement\n    kind: agent\n    agent: dev\n  - id: gate\n    kind: gate\n    on_fail: implement\n  - id: human_gate\n    kind: human_gate\n  - id: pr\n    kind: pr\n`,
  },
};

// ── Settings mock ─────────────────────────────────────────────────────────────

export const mockSettings: SettingsPayload = {
  mcp: [
    { name: "GitHub", url: "https://api.github.com", token: "••••••••" },
  ],
  agent_auth: {
    mode: "oauth_token",
    secret: "••••••••",
  },
  sandbox: {
    runtime: "docker · gVisor (runsc)",
    image: "vibeforge-agent",
  },
  merge_policy: {
    low_risk: "automerge",
    high_risk: "human_gate",
  },
};

// ── Metrics mock ──────────────────────────────────────────────────────────────

export const mockMetrics: MetricsPayload = {
  total_cost_usd: 3.42,
  cost_by_workflow: {
    "factory-plus": 2.1,
    factory: 0.9,
    gated: 0.42,
  },
  cost_by_step: {
    implement: 2.4,
    review: 0.6,
    gate: 0.0,
    verify: 0.42,
    pr: 0.0,
  },
  acceptance_rate: 0.86,
  by_status: {
    DONE: 3,
    AWAITING: 1,
    RUNNING: 1,
    QUEUED: 0,
    FAILED: 0,
    CANCELLED: 0,
  },
};

// ── Epics mock ────────────────────────────────────────────────────────────────

export const mockEpics: Epic[] = [
  { id: "EPIC-1", title: "Utilidades de string" },
  { id: "EPIC-2", title: "Validadores panameños" },
];

// ── Projects mock (Studio) ───────────────────────────────────────────────────
// Cada Project es una entrada en POST /projects. El repo es la URL https de GitHub.

export const mockProjects: Project[] = [
  {
    id: "proj_001",
    name: "turnos-clinica",
    description: "Plataforma de gestión de turnos para clínicas — reserva online, alertas SMS, panel admin",
    repo: "https://github.com/vibeforge-demo/turnos-clinica",
  },
  {
    id: "proj_002",
    name: "asistencia-escolar",
    description: "App de registro de asistencia escolar con QR y notificaciones a padres",
    repo: "https://github.com/vibeforge-demo/asistencia-escolar",
  },
  {
    id: "proj_003",
    name: "marketplace-limpieza",
    description: "Marketplace de servicios de limpieza residencial",
    repo: "https://github.com/vibeforge-demo/marketplace-limpieza",
  },
];

// ── Design runs mock (Studio) ─────────────────────────────────────────────────
// Cada DesignRun es un run del workflow "design" en el kernel. Las fases se
// derivan de los pasos: discovery/prd/architecture/ui/backlog + sus gates.

export const mockDesignRuns: DesignRun[] = [
  {
    id: "run_design_001",
    workflow_id: "design",
    status: "AWAITING",
    idea: "Plataforma de gestión de turnos para clínicas — reserva online, alertas SMS, panel admin",
    created_at: Date.now() - 3600_000,
    project_id: "proj_001",
    repo: "https://github.com/vibeforge-demo/turnos-clinica",
    phases: [
      { stepId: "discovery", name: "Descubrimiento", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "prd", name: "PRD", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "architecture", name: "Arquitectura", designStatus: "DONE", gateStatus: "AWAITING" },
      { stepId: "ui", name: "UI / Pantallas", designStatus: "QUEUED", gateStatus: "QUEUED" },
      { stepId: "backlog", name: "Backlog", designStatus: "QUEUED", gateStatus: "QUEUED" },
      { stepId: "handoff", name: "Handoff → stories", designStatus: "QUEUED", gateStatus: "QUEUED" },
    ],
  },
  {
    id: "run_design_002",
    workflow_id: "design",
    status: "DONE",
    idea: "App de registro de asistencia escolar con QR y notificaciones a padres",
    created_at: Date.now() - 86400_000,
    project_id: "proj_002",
    repo: "https://github.com/vibeforge-demo/asistencia-escolar",
    phases: [
      { stepId: "discovery", name: "Descubrimiento", gateId: "discovery_gate", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "prd", name: "PRD", gateId: "prd_gate", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "architecture", name: "Arquitectura", gateId: "arch_gate", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "ui", name: "UI / Pantallas", gateId: "ui_gate", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "backlog", name: "Backlog", gateId: "backlog_gate", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "handoff", name: "Handoff → stories", gateId: "", designStatus: "DONE", gateStatus: "DONE" },
    ],
  },
  {
    id: "run_design_003",
    workflow_id: "design",
    status: "RUNNING",
    idea: "Marketplace de servicios de limpieza residencial",
    created_at: Date.now() - 1800_000,
    project_id: "proj_003",
    repo: "https://github.com/vibeforge-demo/marketplace-limpieza",
    phases: [
      { stepId: "discovery", name: "Descubrimiento", gateId: "discovery_gate", designStatus: "DONE", gateStatus: "DONE" },
      { stepId: "prd", name: "PRD", gateId: "prd_gate", designStatus: "RUNNING", gateStatus: "QUEUED" },
      { stepId: "architecture", name: "Arquitectura", gateId: "arch_gate", designStatus: "QUEUED", gateStatus: "QUEUED" },
      { stepId: "ui", name: "UI / Pantallas", gateId: "ui_gate", designStatus: "QUEUED", gateStatus: "QUEUED" },
      { stepId: "backlog", name: "Backlog", gateId: "backlog_gate", designStatus: "QUEUED", gateStatus: "QUEUED" },
      { stepId: "handoff", name: "Handoff → stories", gateId: "", designStatus: "QUEUED", gateStatus: "QUEUED" },
    ],
  },
];

// Artefactos por run + paso en modo mock. result.text es el doc markdown.
export const mockArtifacts: Record<string, Record<string, string>> = {
  run_design_001: {
    discovery: `# Descubrimiento — Plataforma de turnos para clínicas

## Problema
Las clínicas pequeñas gestionan turnos por teléfono y WhatsApp. Alta tasa de no-shows (~28 %), sin historial centralizado.

## Usuarios objetivo
- **Pacientes** (móvil): reserva, confirmación, recordatorio.
- **Recepcionistas**: panel web para ver/mover citas.
- **Médicos**: vista diaria de agenda.

## Restricciones descubiertas
- Integrarse con WhatsApp Business API (opcional v2).
- HIPAA-lite: sin diagnósticos almacenados en v1.
- Multilingüe: español + inglés desde el lanzamiento.

## Hipótesis validadas
1. El canal SMS/WhatsApp reduce no-shows un 40 %.
2. El panel admin reemplaza hojas de cálculo en 2 semanas.

## Decisiones bloqueadas hasta PRD
- Stack de notificaciones (Twilio vs. AWS SNS).
- Modelo de precio (SaaS mensual vs. por-cita).
`,
    prd: `# PRD — Plataforma de turnos v1

## Objetivo de negocio
Reducir no-shows ≥35 % y eliminar la gestión manual de agenda en clínicas con 1-5 médicos.

## Alcance v1
| Feature | In | Out |
|---|---|---|
| Reserva online | ✓ | |
| Recordatorio SMS | ✓ | |
| Panel admin (web) | ✓ | |
| App móvil | | ✓ (v2) |
| WhatsApp Business | | ✓ (v2) |

## Métricas de éxito
- No-show rate < 15 % al mes 3.
- Tiempo medio de reserva < 90 s.
- Adopción de panel admin > 80 % de recepcionistas en semana 2.

## Requisitos funcionales
1. El paciente elige médico, fecha y hora desde el link de la clínica.
2. Confirmación por email y SMS inmediata.
3. Recordatorio SMS 24 h antes.
4. La recepcionista puede mover/cancelar desde el panel.
5. Vista diaria del médico (solo lectura en v1).
`,
    architecture: `# Arquitectura — Plataforma de turnos v1

## Stack
- **Frontend**: Next.js 15 (App Router) + Tailwind + shadcn/ui
- **Backend**: FastAPI (Python 3.12) · PostgreSQL 16 · Redis (jobs)
- **Notificaciones**: Twilio SMS (simplicity over SNS en v1)
- **Infra**: Railway (monorepo, zero-ops)

## Módulos
\`\`\`
┌─────────────────────────────────────────────┐
│ Next.js UI (app/)                           │
│  /book      — reserva pública               │
│  /admin     — panel recepcionista / médico  │
└──────────────────┬──────────────────────────┘
                   │ REST + WS
┌──────────────────▼──────────────────────────┐
│ FastAPI                                      │
│  /appointments  CRUD                         │
│  /notify        Twilio wrapper               │
│  /slots         disponibilidad               │
└──────────────────┬──────────────────────────┘
                   │
        ┌──────────┴──────────┐
        │ PostgreSQL           │ Redis (queue)
        │ appointments         │ notify_jobs
        │ doctors              │
        │ patients             │
        └──────────────────────┘
\`\`\`

## Decisiones ADR
- **ADR-01**: Railway sobre Fly.io → deploy sin config de infraestructura, suficiente para v1.
- **ADR-02**: Twilio sobre AWS SNS → SDK simple, precios por uso, sin setup de SES.
- **ADR-03**: Redis para jobs → evita Celery; bull-queue sería v2.
`,
  },
  run_design_002: {
    discovery: `# Descubrimiento — Registro de asistencia escolar

## Problema
Los colegios registran asistencia en papel o en sistemas legacy. Padres no reciben notificación de ausencias hasta el final del día.

## Solución propuesta
App QR: el docente escanea el código del alumno al inicio de clase. El sistema detecta ausencias y notifica al padre/tutor en tiempo real.
`,
    prd: `# PRD — Asistencia escolar QR v1

Alcance mínimo: app docente (iOS/Android), portal de padres (web), notificaciones push.
`,
    architecture: `# Arquitectura — Asistencia escolar QR

Stack: Flutter (app docente + padre) · Firebase (Firestore, Cloud Functions, FCM).
`,
    ui: `# UI / Pantallas — Asistencia escolar QR

## Pantallas principales
1. **App docente**: escáner QR → lista de asistencia → confirmar.
2. **Portal padres**: dashboard de asistencia del hijo · historial mensual.
3. **Admin colegio**: gestión de cursos, docentes, alumnos.
`,
    backlog: `# Backlog — Asistencia escolar QR

## Wave 1 (MVP — 3 semanas)
- S1-01: Auth docente (Firebase Auth)
- S1-02: Escáner QR (Flutter camera)
- S1-03: Registro en Firestore
- S1-04: Notificación FCM al padre
- S1-05: Portal web padres (Next.js)

## Wave 2
- S2-01: Reportes mensuales PDF
- S2-02: Integración con sistema de notas
`,
    handoff: `# Handoff → stories

23 tickets creados en el store nativo. Asignados a Wave 1 (MVP) y Wave 2.
`,
  },
  run_design_003: {
    discovery: `# Descubrimiento — Marketplace de limpieza residencial

## Problema
Encontrar servicio de limpieza confiable requiere recomendaciones boca a boca; no hay plataforma local de confianza en LATAM.

## Hipótesis
- El proveedor individual tiene mayor flexibilidad de horario que las empresas.
- El cliente prioriza reputación (ratings) sobre precio.

*(PRD en progreso…)*
`,
  },
};

// ── Orchestrator tickets mock ─────────────────────────────────────────────────

export const mockOrchestratorTickets: OrchestratorTicket[] = [
  { id: "ENG-1", title: "is_valid_email(s) + tests", status: "done", deps: [], run_id: "run_f7932443" },
  { id: "ENG-2", title: "to_roman(n) 1..3999 + tests", status: "done", deps: [], run_id: "run_69c573fc" },
  { id: "ENG-3", title: "fib(n) + tests", status: "done", deps: ["ENG-1"], run_id: "run_e6bca38f" },
  { id: "ENG-12", title: "reverse_words(s) — reordena palabras, colapsa espacios", status: "running", deps: [], run_id: "run_76af79df" },
  { id: "ENG-14", title: "Validador de cédula panameña + tests", status: "running", deps: ["ENG-1"], run_id: "run_a91c20e1" },
  { id: "ENG-15", title: "Formato de fecha panameño + tests", status: "backlog", deps: ["ENG-14"] },
];
