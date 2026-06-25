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

export interface SettingsPayload {
  mcp: McpConnection[];
  agent_auth: AgentAuth;
  sandbox: {
    runtime: string;
    image: string;
  };
  merge_policy: {
    low_risk: string;   // "automerge"
    high_risk: string;  // "human_gate"
  };
  execution_unit: string; // "sprint" (default, 1 PR/sprint) | "story" (1 PR/story)
  merge_mode: string; // "manual" (default, human merges PR) | "auto" (factory merges)
}

// ── Metrics / Spend ───────────────────────────────────────────────────────────
// GET /metrics → MetricsPayload

export interface MetricsPayload {
  total_cost_usd: number;
  cost_by_workflow: Record<string, number>;
  cost_by_step: Record<string, number>;
  acceptance_rate: number; // 0..1
  by_status: Record<string, number>;
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

export interface OrchestratorTicket {
  id: string;
  title: string;
  status: TicketStatus;
  deps: string[];
  run_id?: string;
}
