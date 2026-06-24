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
