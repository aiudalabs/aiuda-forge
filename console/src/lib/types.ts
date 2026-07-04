// Tipos del dominio — modelan el contrato genérico del kernel v2 (doc 14): runs / steps /
// events / artifacts. La UI es render del contrato; estos tipos son la forma que esperamos de
// GET /runs, GET /runs/{id} y del event bus. Si el kernel devuelve campos extra, se ignoran.

export type RunStatus =
  | "QUEUED"
  | "RUNNING"
  | "AWAITING"
  | "DONE"
  | "FAILED"
  | "CANCELLED";

export type StepStatus =
  | "QUEUED"
  | "RUNNING"
  | "DONE"
  | "FAILED"
  | "AWAITING"
  | "SKIPPED";

// Tipo de paso del workflow (doc 16 §2.5): agent | gate | agentic_verify | human_gate | pr
export type StepKind =
  | "agent"
  | "gate"
  | "agentic_verify"
  | "human_gate"
  | "pr"
  | string;

export interface Ticket {
  id: string; // ENG-12
  title: string;
}

export interface RunStep {
  id: string; // "implement", "gate", "review", "human_gate", "pr"
  kind: StepKind;
  status: StepStatus;
  agent?: string; // "dev", "reviewer", "verifier"
  model?: string; // "opus 4.8", "sonnet 4.6"
  detail?: string; // salida del gate, veredicto del review, etc.
  cost?: number; // $ acumulado del paso
  durationMs?: number;
}

export interface RunBadge {
  kind: "gate" | "review" | "sandbox" | "verify";
  label: string; // "gate ✓ 6 tests"
  tone?: "ok" | "warn" | "info";
}

export interface RunCost {
  total: number;
  byStep?: { step: string; cost: number }[];
  tokensIn?: number;
  tokensOut?: number;
}

export interface Run {
  id: string; // run_76af79df
  ticket: Ticket;
  workflow: string; // factory | factory-plus | gated
  status: RunStatus;
  agent?: string; // dev
  model?: string; // opus
  currentStep?: string; // "implement"
  cost: number; // total acumulado
  badges: RunBadge[];
  dependsOn?: string[]; // ["ENG-1"]
  pr?: { number: number; url?: string };
  awaitingStep?: string; // step id que está en human_gate
  sandbox?: string; // "docker · egress allowlist"
  createdAt?: string;
  project?: string;
}

export interface RunDetail extends Run {
  steps: RunStep[];
  diff?: string; // diff propuesto (texto unificado)
  costBreakdown?: RunCost;
}

// ── Eventos del bus (doc 14 §B) ───────────────────────────────────────────────
export type RunEventType =
  | "run.created"
  | "run.status_changed"
  | "step.status_changed"
  | "step.event"
  | "step.gate"
  | "step.verify"
  | "run.awaiting_approval"
  | "run.done"
  | "run.failed"
  | "run.cancelled";

export interface RunEvent {
  id: number; // monotónico, para ?after=
  runId: string;
  ts: string; // ISO o "12:04:02"
  type: RunEventType;
  step?: string;
  message?: string; // texto renderizable del live-log
  data?: Record<string, unknown>;
}

// ── Stats del board ───────────────────────────────────────────────────────────
export interface BoardStats {
  running: number;
  awaiting: number;
  openPRs: number;
  projectCost: number;
}

// ── Control plane ─────────────────────────────────────────────────────────────
export interface ControlStatus {
  paused: boolean;
}

// ── Notificaciones (campana) ──────────────────────────────────────────────────
export interface Notification {
  id: string;
  runId: string;
  kind: "awaiting" | "failed";
  title: string;
  ts: string;
}

// ── Registry ──────────────────────────────────────────────────────────────────
// GET /registry/{kind} → { ids: string[] }
// GET /registry/{kind}/{id} → raw YAML or markdown string
// PUT /registry/{kind}/{id} → { saved: string }
// DELETE /registry/{kind}/{id} → { deleted: string }

export type RegistryKind = "agents" | "skills" | "workflows";

export interface RegistryListResponse {
  ids: string[];
}

export interface RegistrySaveResponse {
  saved: string;
}

export interface RegistryDeleteResponse {
  deleted: string;
}

// ── Settings ──────────────────────────────────────────────────────────────────
// GET /settings → SettingsPayload (secrets masked as "••••••••")
// PUT /settings → same shape; send masked value back when user didn't change it

export interface McpConnection {
  name: string;
  url: string;
  token: string; // may be "••••••••" when masked
}

export interface AgentAuth {
  mode: string; // "oauth_token" | "api_key"
  secret: string; // may be "••••••••" when masked
}

// GLOBAL settings (por instancia del control-plane): solo conexiones · seguridad ·
// sandbox · CORS. execution_unit, merge_mode y merge_policy salieron de aquí en Wave 2
// → ahora son PER-PROYECTO (ver ProjectSettings, GET/PUT /projects/{id}/settings).
export interface SettingsPayload {
  mcp: McpConnection[];
  agent_auth: AgentAuth;
  sandbox: {
    runtime: string;
    image: string;
  };
}

// PER-PROJECT settings (GET/PUT /projects/{id}/settings, Wave 2 + pivote F2). Cómo
// la fábrica ejecuta el trabajo de ESTE proyecto: la unidad de PR, quién mergea, y
// (pivote GitHub-native) cómo/con qué se despacha el trabajo a los agentes de GitHub.
export interface ProjectSettings {
  execution_unit: "sprint" | "story"; // "sprint" (default, 1 PR/sprint) | "story" (1 PR/story)
  merge_mode: "manual" | "auto"; // "manual" (default, humano mergea) | "auto" (la fábrica mergea)
  dispatch_mode: "approve" | "auto" | "off"; // "approve" (default, humano confirma) | "auto" | "off"
  executor: "copilot" | "claude_action"; // canal de ejecución (default "copilot")
  model_by_lane: Record<string, string>; // lane → modelo (vacío = modelo auto)
  // Aprobación de los workflows action_required de los PRs de agentes. "manual"
  // (default): un humano clickea "Approve and run workflows" en cada PR. "auto_if_safe":
  // el conductor los aprueba solo si el diff NO toca .github/workflows/**.
  workflow_approval: "manual" | "auto_if_safe";
  // Tope de stories con agente trabajando a la vez. Entero ≥0; 0 = sin límite.
  max_concurrency: number;
}

// ── Despacho a agentes de GitHub (pivote F2) ──────────────────────────────────
// GET /projects/{id}/dispatch/candidates → lo que está listo para despachar YA.
// POST /projects/{id}/dispatch → dispara la story/sprint a un agente de GitHub.

export interface DispatchCandidate {
  kind: "story" | "sprint";
  id: string;          // id de la story o del sprint
  title: string;
  stories: string[];   // ids de las stories del sprint (solo kind="sprint")
  lane: string;        // lane responsable (define el modelo/executor)
  model: string;       // modelo resuelto ("" = auto)
  executor: string;    // canal de ejecución (copilot | claude_action)
}

export interface DispatchCandidates {
  execution_unit: "sprint" | "story";
  candidates: DispatchCandidate[];
}

export interface DispatchResult {
  dispatched: string[]; // ids de las stories despachadas
  channel: string;      // canal usado
  model: string;        // modelo usado ("" = auto)
  task_url?: string;    // URL de la Agent task en GitHub, si el canal la expone
}

// ── Metrics / Spend ───────────────────────────────────────────────────────────
// GET /metrics → MetricsPayload

export interface MetricsPayload {
  total_cost_usd: number;
  cost_by_workflow: Record<string, number>;
  cost_by_step: Record<string, number>;
  acceptance_rate: number; // 0..1
  by_status: Record<string, number>;
  total_tokens_in?: number; // populated for runs since the usage-capture change
  total_tokens_out?: number;
  total_turns?: number; // agent round-trips across all steps
  agent_calls?: number; // number of agent steps that reported usage
}

// GET /projects/{id}/spend/github → gasto medido en GitHub del ciclo actual
// (Copilot premium requests, Actions minutes, LFS, …). Si GitHub no expone la
// facturación para el owner del repo → { available: false, reason }.

export interface GitHubSpendItem {
  product: string;      // "actions" | "copilot" | "git_lfs" | …
  sku: string;
  quantity: number;
  unit_type: string;    // "Minutes" | "Requests" | "GigabyteHours" | …
  gross_amount: number; // USD antes de descuentos/free tier
  net_amount: number;   // USD realmente adeudado
}

export interface GitHubSpend {
  available: boolean;
  reason?: string;                    // por qué no está disponible (available:false)
  cycle?: string;                     // "2026-07" (mes = ciclo de facturación)
  items?: GitHubSpendItem[];          // filas agregadas por SKU
  totals?: Record<string, number>;    // neto por producto: copilot, actions, …
}

// ── Studio / Design runs ──────────────────────────────────────────────────────
// Un DesignRun es un run del workflow "design" en el kernel. La UI del Studio
// lo presenta como un proyecto de diseño guiado por fases.

export type DesignStepStatus = "QUEUED" | "RUNNING" | "DONE" | "AWAITING" | "FAILED";

export interface DesignPhase {
  stepId: string; // "discovery" | "prd" | "architecture" | "ui" | "backlog" | "handoff"
  name: string;   // "Descubrimiento", "PRD", etc.
  gateId?: string; // id del human_gate de la fase ("arch_gate" ≠ "architecture_gate"); "" / undefined si no tiene
  designStatus: DesignStepStatus; // estado del paso de diseño (el agente)
  gateStatus: DesignStepStatus;   // estado del gate (human_gate)
}

export interface DesignRun {
  id: string;
  workflow_id: string; // "design" (ciclo completo) o "iterate" (backlog delta)
  status: RunStatus;
  idea: string;        // .payload.instructions — la descripción del proyecto
  created_at: number;  // epoch ms
  phases: DesignPhase[];
  project_id?: string; // .payload.project_id — vincula al proyecto GitHub
  repo?: string;       // .payload.repo — URL https del repo
}

// ── Projects ──────────────────────────────────────────────────────────────────
// POST /projects → { id, name, description, repo }
// GET  /projects → { projects: Project[] }

export interface Project {
  id: string;
  name: string;
  description: string;
  repo: string; // GitHub https URL
  owner_id?: string;
}

// ── Members & invitations (v1.2 roles) ────────────────────────────────────────
export type Role = "owner" | "editor" | "viewer";

export interface Member {
  user_id: string;
  email: string;
  role: Role;
}

export interface Invite {
  token: string;
  project_id: string;
  email: string;
  role: Role;
  created_at: number;
  accepted_at: number;
}

export interface MembersPayload {
  members: Member[];
  invites: Invite[];
}

// ── Channels (v1.3) ───────────────────────────────────────────────────────────
export interface Channel {
  project_id: string;
  connector: string;
  target: string;
  events: string;
  created_at: number;
}

// ── Project docs (Studio = Confluence, U1) ────────────────────────────────────
// GET /projects/{id}/docs → { docs: DocEntry[], ref }
// GET /projects/{id}/docs/file?path=docs/PRD.md → { content }
// The specs live in the repo (docs/ on the dev branch), the persistent source of
// truth that outlives an ephemeral design run.
export interface DocEntry {
  name: string; // "PRD.md"
  path: string; // "docs/PRD.md"
  type: "file" | "dir";
  size: number;
}

// ── Tickets (from orchestrator) ───────────────────────────────────────────────
// GET /tickets → { tickets: Ticket[] }
// GET /epics   → { epics: Epic[] }
// POST /stories → OrchestratorTicket

// Estados del store nativo de stories (engine/internal/tickets): el orquestador
// deriva "ready" de un "backlog" cuyas deps están "done".
export type TicketStatus = "backlog" | "ready" | "running" | "in_review" | "done" | "failed";

export interface Epic {
  id: string;
  title: string;
}

// A planned sprint: a coherent, demoable increment (one PR). name + goal come from
// the scrum-master's backlog (e.g. "Sprint 1 — Platform Foundation").
export interface Sprint {
  id: string;
  name: string;
  goal: string;
}

export interface OrchestratorTicket {
  id: string;
  title: string;
  body?: string;        // skeleton user-story ("As a X, I want Y…") — what the story is
  acceptance?: string;  // falsifiable acceptance-criteria lines
  status: TicketStatus;
  deps: string[];
  run_id?: string;
  sprint_id?: string;   // the sprint this story belongs to (SP1, SP2, …)
  epic_id?: string;     // parent epic (E1, E2, …)
  owner?: string;       // agent lane responsible (python-dev, react-dev, …) — the "assignee"
  pr_url?: string;      // PR opened by the story's run, when one exists
  session_url?: string; // GitHub agent session executing it (Copilot task / Actions run)
  external_ref?: string; // espejo GitHub (github:owner/repo#N) — presente = story exportada
  repo?: string;        // owner/repo the story lands in
}

// ─── Vista Agentes (pivote GitHub-native, PLAN §4 "Vistas nuevas") ───────────
// Un workflow run de GitHub detenido en `action_required`: espera aprobación
// humana antes de correr (primer run de un contributor, o política de seguridad
// cuando el PR toca .github/workflows/**).
export interface WorkflowRunRef {
  id: string;   // id del run (para POST approve)
  name: string; // nombre del workflow ("CI", "claude-review", …)
}

// Un PR abierto del repo del proyecto, con las stories que le dieron origen y
// los workflow runs que esperan aprobación. Proyección del conductor
// (GET /projects/{id}/prs).
export interface ProjectPR {
  number: number;
  title: string;
  url: string;
  draft: boolean;
  author: string;                       // login del autor (bot o humano)
  merge_state?: string;                 // "conflicting" | "clean" | "" (GitHub aún computa)
  stories: string[];                    // ids de stories ligadas a este PR
  action_required_runs: WorkflowRunRef[];
}

// Resultado de despachar una resolución de conflicto. dispatched=false + reason
// cuando el guard anti-loop lo bloquea (ya hay una resolución en vuelo).
export interface ResolveConflictsResult {
  dispatched: boolean;
  reason?: string;
}

// Resultado de aprobar un workflow run. safe=false + reason cuando la política
// lo bloquea (el diff toca .github/workflows/**).
export interface ApproveWorkflowResult {
  approved: boolean;
  safe: boolean;
  reason?: string;
}

/** Un canal de ejecución y su disponibilidad real en el GitHub del proyecto. */
export interface ExecutorInfo {
  id: string; // "copilot" | "claude_action"
  available: boolean;
  reason?: string;
  default: boolean;
}

// ── Conexión GitHub (Settings → Conexiones) ───────────────────────────────────
// GET /auth/github/status → ¿este usuario tiene GitHub conectado? + si la App de
// la instancia ya está configurada (manifest flow completado).
export interface GitHubStatus {
  connected: boolean;
  login?: string;
  app_configured?: boolean;
  app_url?: string;
}
