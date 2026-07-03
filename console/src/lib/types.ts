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

// PER-PROJECT settings (GET/PUT /projects/{id}/settings, Wave 2). Cómo la fábrica
// ejecuta el trabajo de ESTE proyecto: la unidad de PR y quién mergea.
export interface ProjectSettings {
  execution_unit: "sprint" | "story"; // "sprint" (default, 1 PR/sprint) | "story" (1 PR/story)
  merge_mode: "manual" | "auto"; // "manual" (default, humano mergea) | "auto" (la fábrica mergea)
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
  workflow_id: string; // siempre "design"
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
  repo?: string;        // owner/repo the story lands in
}
