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
  ProjectSettings,
  Run,
  RunDetail,
  RunEvent,
  SettingsPayload,
} from "./types";

// Proyecto activo por defecto en modo mock = el primer proyecto de mockProjects.
// Los runs/tickets de ejemplo cuelgan de este id para que el switcher multi-tenant
// (Wave 2) muestre datos al filtrar por ?project=proj_001.
export const MOCK_PROJECT = "proj_001";

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
  // Runs de OTRO proyecto (proj_002) — demuestran el scoping del switcher: solo
  // aparecen cuando el proyecto activo es asistencia-escolar.
  {
    id: "run_b3d10a77",
    ticket: { id: "S1-02", title: "Escáner QR — Flutter camera plugin" },
    workflow: "factory-plus",
    status: "RUNNING",
    agent: "dev",
    model: "opus",
    currentStep: "implement",
    cost: 0.22,
    badges: [],
    project: "proj_002",
  },
  {
    id: "run_c8e21b90",
    ticket: { id: "S1-01", title: "Auth docente — Firebase Auth email/password" },
    workflow: "factory",
    status: "DONE",
    agent: "dev",
    model: "opus",
    cost: 0.27,
    pr: { number: 1 },
    badges: [{ kind: "gate", label: "gate ✓", tone: "ok" }],
    project: "proj_002",
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
};

// ── Per-project settings mock ─────────────────────────────────────────────────
// GET/PUT /projects/{id}/settings. execution_unit + merge_mode por proyecto.
export const mockProjectSettings: Record<string, ProjectSettings> = {
  proj_001: { execution_unit: "sprint", merge_mode: "manual", dispatch_mode: "approve", executor: "copilot", model_by_lane: {} },
  proj_002: { execution_unit: "story", merge_mode: "auto", dispatch_mode: "auto", executor: "claude_action", model_by_lane: { "python-dev": "claude-sonnet-4.6" } },
  proj_003: { execution_unit: "sprint", merge_mode: "manual", dispatch_mode: "approve", executor: "copilot", model_by_lane: {} },
};

// Default para un proyecto que aún no tiene settings guardados.
export const DEFAULT_PROJECT_SETTINGS: ProjectSettings = {
  execution_unit: "sprint",
  merge_mode: "manual",
  dispatch_mode: "approve",
  executor: "copilot",
  model_by_lane: {},
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
      { stepId: "mockups", name: "Mockups", gateId: "mockups_gate", designStatus: "QUEUED", gateStatus: "QUEUED" },
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
      { stepId: "mockups", name: "Mockups", gateId: "mockups_gate", designStatus: "DONE", gateStatus: "DONE" },
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
      { stepId: "mockups", name: "Mockups", gateId: "mockups_gate", designStatus: "QUEUED", gateStatus: "QUEUED" },
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
    backlog: `epic:
  id: E1
  title: "asistencia-escolar — MVP QR"
  description: >
    Registro de asistencia escolar por QR. El docente escanea el código del alumno
    al inicio de clase; el sistema detecta ausencias y notifica al padre en tiempo real.
stories:
  - id: S1-01
    title: "Auth docente — Firebase Auth email/password"
    body: |
      Implementar pantalla de login para docentes usando Firebase Auth.
      La sesión persiste entre reinicios de la app.
    acceptance: |
      - Login con email/password funciona.
      - Token persiste; la app no pide login al reiniciar.
      - Error claro si credenciales incorrectas.
    owner: flutter-dev
    deps: []
  - id: S1-02
    title: "Escáner QR — Flutter camera plugin"
    body: |
      Pantalla de escaneo QR usando flutter_barcode_scanner.
      Decodifica el id del alumno y llama al API de registro.
    acceptance: |
      - Abre la cámara al entrar a la pantalla.
      - Detecta QR y muestra nombre del alumno en 1 s.
      - Botón de confirmación antes de registrar.
    owner: flutter-dev
    deps: [S1-01]
  - id: S1-03
    title: "Registro en Firestore — colección attendance"
    body: |
      Cloud Function callable \`recordAttendance\` que escribe en
      Firestore: colección attendance/{date}/records/{studentId}.
    acceptance: |
      - Registro se persiste en < 500 ms.
      - Duplicados en el mismo día se ignoran (idempotente).
      - Reglas de Firestore: solo la Cloud Function escribe.
    owner: firebase-dev
    deps: []
  - id: S1-04
    title: "Notificación FCM al padre cuando alumno ausente"
    body: |
      Cloud Function trigger on Firestore write: si el alumno no tiene registro
      de asistencia a los 15 min de inicio de clase, enviar push FCM al padre.
    acceptance: |
      - Push llega al padre en < 30 s de la ausencia.
      - El mensaje incluye nombre del alumno y hora.
      - No se envía si el alumno ya fue marcado presente.
    owner: firebase-dev
    deps: [S1-03]
  - id: S1-05
    title: "Portal web padres — dashboard de asistencia"
    body: |
      Página web Next.js con auth de padres (Firebase Auth).
      Muestra historial de asistencia del hijo por mes.
    acceptance: |
      - Tabla con fecha, hora y estado (presente/ausente).
      - Filtro por mes.
      - Funciona en móvil (responsive).
    owner: react-dev
    deps: [S1-03]
`,
    mockups: `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>Asistencia Escolar — Mockup</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: system-ui, sans-serif; background: #f4f6f9; color: #1a1a2e; }
  .screen { max-width: 390px; margin: 24px auto; background: #fff; border-radius: 20px; overflow: hidden; box-shadow: 0 8px 32px rgba(0,0,0,.12); }
  .statusbar { background: #1a1a2e; color: #fff; padding: 12px 20px; font-size: 12px; display: flex; justify-content: space-between; }
  .header { background: #1a73e8; color: #fff; padding: 20px; }
  .header h1 { font-size: 20px; font-weight: 700; }
  .header p { font-size: 13px; opacity: .8; margin-top: 4px; }
  .content { padding: 20px; }
  .qr-box { border: 3px dashed #1a73e8; border-radius: 16px; aspect-ratio: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; color: #1a73e8; margin-bottom: 20px; }
  .qr-icon { font-size: 64px; }
  .qr-label { font-size: 14px; font-weight: 600; }
  .student-card { background: #e8f0fe; border-radius: 12px; padding: 16px; display: flex; align-items: center; gap: 14px; margin-bottom: 16px; }
  .avatar { width: 48px; height: 48px; border-radius: 50%; background: #1a73e8; color: #fff; display: grid; place-items: center; font-weight: 700; font-size: 18px; flex: none; }
  .student-info h3 { font-size: 16px; font-weight: 700; }
  .student-info p { font-size: 12px; color: #5f6368; }
  .btn { width: 100%; padding: 14px; border: none; border-radius: 12px; font-size: 16px; font-weight: 700; cursor: pointer; }
  .btn.confirm { background: #34a853; color: #fff; }
  .btn.skip { background: #f1f3f4; color: #5f6368; margin-top: 10px; }
  .nav { display: grid; grid-template-columns: repeat(4,1fr); border-top: 1px solid #e8eaed; }
  .nav-item { padding: 12px 8px; text-align: center; font-size: 10px; color: #5f6368; cursor: pointer; }
  .nav-item .icon { font-size: 22px; display: block; margin-bottom: 2px; }
  .nav-item.active { color: #1a73e8; }
</style>
</head>
<body>
<div class="screen">
  <div class="statusbar"><span>9:41</span><span>●●● WiFi 100%</span></div>
  <div class="header">
    <h1>Registro de Asistencia</h1>
    <p>Matemáticas · 7A · Lun 24 Jun, 9:00</p>
  </div>
  <div class="content">
    <div class="qr-box">
      <span class="qr-icon">⬛</span>
      <span class="qr-label">Escanear código QR del alumno</span>
    </div>
    <div class="student-card">
      <div class="avatar">AM</div>
      <div class="student-info">
        <h3>Ana Martínez</h3>
        <p>ID: 2024-0312 · 7° Grado A</p>
      </div>
    </div>
    <button class="btn confirm">Marcar Presente</button>
    <button class="btn skip">Omitir / Ausente</button>
  </div>
  <div class="nav">
    <div class="nav-item active"><span class="icon">📷</span>Escanear</div>
    <div class="nav-item"><span class="icon">📋</span>Lista</div>
    <div class="nav-item"><span class="icon">📊</span>Reporte</div>
    <div class="nav-item"><span class="icon">⚙️</span>Config</div>
  </div>
</div>
</body>
</html>
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

// Tickets de ejemplo por proyecto (el backend filtra server-side con ?project=;
// en mock filtramos por este map). mockOrchestratorTickets queda como el set del
// proyecto por defecto para compatibilidad con call-sites sin proyecto.
export const mockTicketsByProject: Record<string, OrchestratorTicket[]> = {
  proj_001: [
    { id: "ENG-1", title: "is_valid_email(s) + tests", status: "done", deps: [], run_id: "run_f7932443" },
    { id: "ENG-2", title: "to_roman(n) 1..3999 + tests", status: "done", deps: [], run_id: "run_69c573fc" },
    { id: "ENG-3", title: "fib(n) + tests", status: "done", deps: ["ENG-1"], run_id: "run_e6bca38f" },
    { id: "ENG-12", title: "reverse_words(s) — reordena palabras, colapsa espacios", status: "running", deps: [], run_id: "run_76af79df" },
    { id: "ENG-14", title: "Validador de cédula panameña + tests", status: "running", deps: ["ENG-1"], run_id: "run_a91c20e1" },
    { id: "ENG-15", title: "Formato de fecha panameño + tests", status: "backlog", deps: ["ENG-14"] },
  ],
  proj_002: [
    { id: "S1-01", title: "Auth docente — Firebase Auth email/password", status: "done", deps: [], run_id: "run_c8e21b90" },
    { id: "S1-02", title: "Escáner QR — Flutter camera plugin", status: "running", deps: ["S1-01"], run_id: "run_b3d10a77" },
    { id: "S1-03", title: "Registro en Firestore — colección attendance", status: "ready", deps: [] },
    { id: "S1-04", title: "Notificación FCM al padre cuando alumno ausente", status: "backlog", deps: ["S1-03"] },
  ],
  proj_003: [],
};

export const mockOrchestratorTickets: OrchestratorTicket[] = mockTicketsByProject[MOCK_PROJECT];
