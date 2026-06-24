// Capa única de cliente de API. Lee VIBEFORGE_API_URL (lib/config) y habla con el control-plane
// del kernel v2 por HTTP. Toda acción de UI = un endpoint del contrato (doc 14). Ninguna lógica
// de negocio del flujo vive aquí: solo fetch + tipos.
//
// MODO MOCK: si la API no responde (o NEXT_PUBLIC_FORCE_MOCK=1) caemos a datos de ejemplo
// (lib/mock) para que la UI se construya/vea sin el backend arriba. `getApiMode()` expone el
// modo activo para que la UI lo muestre y para que el WS sepa si conectarse.

import { API_URL, FORCE_MOCK, HEALTH_TIMEOUT_MS } from "./config";
import {
  MOCK_PROJECT,
  mockControl,
  mockEvents,
  mockNotifications,
  mockRunDetails,
  mockRuns,
  mockSpendToday,
  mockStats,
} from "./mock";
import type {
  BoardStats,
  ControlStatus,
  Notification,
  Run,
  RunDetail,
  RunEvent,
  RunStatus,
} from "./types";

export type ApiMode = "real" | "mock";

let modePromise: Promise<ApiMode> | null = null;

async function probe(): Promise<ApiMode> {
  if (FORCE_MOCK) return "mock";
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), HEALTH_TIMEOUT_MS);
    const res = await fetch(`${API_URL}/healthz`, { signal: ctrl.signal });
    clearTimeout(t);
    return res.ok ? "real" : "mock";
  } catch {
    return "mock";
  }
}

/** Modo activo (cacheado tras la primera prueba de salud). */
export function getApiMode(): Promise<ApiMode> {
  if (!modePromise) modePromise = probe();
  return modePromise;
}

/** Fuerza re-detección (p.ej. tras un toggle manual). */
export function resetApiMode() {
  modePromise = null;
}

async function isMock() {
  return (await getApiMode()) === "mock";
}

async function http<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `${init?.method || "GET"} ${path} → ${res.status} ${body}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Lectura
// ─────────────────────────────────────────────────────────────────────────────

export async function listRuns(params?: { status?: RunStatus; project?: string }): Promise<Run[]> {
  if (await isMock()) {
    let runs = [...mockRuns];
    if (params?.status) runs = runs.filter((r) => r.status === params.status);
    return runs;
  }
  const qs = new URLSearchParams();
  if (params?.status) qs.set("status", params.status);
  if (params?.project) qs.set("project", params.project);
  const q = qs.toString();
  return http<Run[]>(`/runs${q ? `?${q}` : ""}`);
}

export async function getRun(id: string): Promise<RunDetail> {
  if (await isMock()) {
    const d = mockRunDetails[id];
    if (!d) throw new ApiError(404, `run ${id} no encontrado (mock)`);
    return d;
  }
  return http<RunDetail>(`/runs/${id}`);
}

export async function getEvents(id: string, after = 0): Promise<RunEvent[]> {
  if (await isMock()) {
    return (mockEvents[id] || []).filter((e) => e.id > after);
  }
  return http<RunEvent[]>(`/runs/${id}/events?after=${after}`);
}

export async function getStats(): Promise<BoardStats> {
  if (await isMock()) return mockStats;
  // GET /metrics con desglose; tomamos el resumen del board.
  return http<BoardStats>(`/metrics`);
}

export interface SpendToday {
  cost: number;
  tokens: string;
}

export async function getSpendToday(): Promise<SpendToday> {
  if (await isMock()) return mockSpendToday;
  // TODO(endpoint): mapear GET /metrics → gasto del día + tokens (doc 16 §2.6).
  return http<SpendToday>(`/metrics`);
}

export async function getControlStatus(): Promise<ControlStatus> {
  if (await isMock()) return mockControl;
  return http<ControlStatus>(`/control/status`);
}

export async function getNotifications(): Promise<Notification[]> {
  // Derivadas de runs en AWAITING/FAILED — el kernel no expone /notifications todavía, así que
  // la campana refleja el estado vivo del board (mock o real). TODO(endpoint): GET /notifications
  // o derivar del stream del bus (run.awaiting_approval / run.failed).
  const runs = await listRuns();
  const derived = runs
    .filter((r) => r.status === "AWAITING" || r.status === "FAILED")
    .map((r) => {
      const seed = mockNotifications.find((n) => n.runId === r.id);
      return {
        id: r.id,
        runId: r.id,
        kind: r.status === "AWAITING" ? ("awaiting" as const) : ("failed" as const),
        title: seed?.title || `${r.ticket.id} · ${r.ticket.title}`,
        ts: seed?.ts || r.createdAt || "",
      };
    });
  return derived;
}

export const DEFAULT_PROJECT = MOCK_PROJECT;

// ─────────────────────────────────────────────────────────────────────────────
// Acciones (mutaciones) — cada una = un endpoint del contrato
// ─────────────────────────────────────────────────────────────────────────────

export async function createRun(input: { workflow: string; payload: Record<string, unknown> }): Promise<Run> {
  if (await isMock()) {
    const id = `run_mock_${mockRuns.length + 1}`;
    const run: Run = {
      id,
      ticket: { id: (input.payload.ticket as string) || "ENG-?", title: (input.payload.title as string) || "Nuevo run" },
      workflow: input.workflow,
      status: "QUEUED",
      cost: 0,
      badges: [],
      project: MOCK_PROJECT,
    };
    mockRuns.unshift(run);
    return run;
  }
  return http<Run>(`/runs`, { method: "POST", body: JSON.stringify(input) });
}

export async function cancelRun(id: string): Promise<void> {
  if (await isMock()) return mutateMockStatus(id, "CANCELLED");
  await http<void>(`/runs/${id}/cancel`, { method: "POST" });
}

export async function retryRun(id: string): Promise<void> {
  if (await isMock()) return mutateMockStatus(id, "RUNNING");
  await http<void>(`/runs/${id}/retry`, { method: "POST" });
}

export async function deleteRun(id: string): Promise<void> {
  if (await isMock()) {
    const i = mockRuns.findIndex((r) => r.id === id);
    if (i >= 0) mockRuns.splice(i, 1);
    return;
  }
  await http<void>(`/runs/${id}`, { method: "DELETE" });
}

export async function approveStep(id: string, step: string): Promise<void> {
  if (await isMock()) return mutateMockStatus(id, "DONE");
  await http<void>(`/runs/${id}/steps/${step}/approve`, { method: "POST" });
}

/**
 * Rechazo con motivo del human_gate. El kernel reencola al implementer como feedback (on_fail).
 * Si el endpoint aún no existe (404), el caller deja el botón como TODO — ver doc 17 §1.
 */
export async function rejectStep(id: string, step: string, reason: string): Promise<void> {
  if (await isMock()) return mutateMockStatus(id, "RUNNING");
  // TODO(endpoint): POST /runs/{id}/steps/{step}/reject {reason} — si 404, reusar retry-con-feedback.
  await http<void>(`/runs/${id}/steps/${step}/reject`, {
    method: "POST",
    body: JSON.stringify({ reason }),
  });
}

export async function pauseFactory(): Promise<void> {
  if (await isMock()) {
    mockControl.paused = true;
    return;
  }
  await http<void>(`/control/pause`, { method: "POST" });
}

export async function resumeFactory(): Promise<void> {
  if (await isMock()) {
    mockControl.paused = false;
    return;
  }
  await http<void>(`/control/resume`, { method: "POST" });
}

function mutateMockStatus(id: string, status: RunStatus) {
  const r = mockRuns.find((x) => x.id === id);
  if (r) {
    r.status = status;
    if (status !== "AWAITING") r.awaitingStep = undefined;
  }
  const d = mockRunDetails[id];
  if (d) d.status = status;
}
