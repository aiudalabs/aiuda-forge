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
  mockArtifacts,
  mockControl,
  mockDesignRuns,
  mockEpics,
  mockEvents,
  mockMetrics,
  mockNotifications,
  mockOrchestratorTickets,
  mockProjects,
  mockRegistryContent,
  mockRegistryIds,
  mockRunDetails,
  mockRuns,
  mockSettings,
  mockSpendToday,
  mockStats,
} from "./mock";
import type {
  BoardStats,
  ControlStatus,
  DesignPhase,
  DesignRun,
  DesignStepStatus,
  McpConnection,
  MetricsPayload,
  Notification,
  OrchestratorTicket,
  Project,
  RegistryDeleteResponse,
  RegistryKind,
  RegistryListResponse,
  RegistrySaveResponse,
  Run,
  RunDetail,
  RunEvent,
  RunStatus,
  RunStep,
  SettingsPayload,
  StepStatus,
  Epic,
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
  // El control-plane envuelve la lista como {runs:[...]}; toleramos también un
  // array pelado por si el contrato cambia.
  const res = await http<KernelRun[] | { runs: KernelRun[] }>(`/runs${q ? `?${q}` : ""}`);
  const raw = Array.isArray(res) ? res : res?.runs ?? [];
  return raw.map(mapRun);
}

// KernelRun es el shape que devuelve el kernel en GET /runs (id, workflow_id,
// status, payload JSON-string, timestamps). El modelo Run de la UI es más rico
// (ticket, cost, badges…); mapRun traduce uno al otro y rellena lo ausente.
interface KernelRun {
  id: string;
  workflow_id?: string;
  status: RunStatus;
  payload?: string;
  created_at?: number;
}

function mapRun(r: KernelRun): Run {
  let issue: number | undefined;
  let ticketText = "";
  let shortTitle = "";
  try {
    const p = JSON.parse(r.payload ?? "{}");
    issue = typeof p.issue === "number" ? p.issue : undefined;
    ticketText = typeof p.ticket === "string" ? p.ticket : "";
    // Un título corto explícito (payload.title, de las stories) gana sobre la
    // primera línea del ticket — así una descripción larga no se vuelve el título.
    if (typeof p.title === "string") shortTitle = p.title;
  } catch {
    /* payload no-JSON: lo dejamos vacío */
  }
  const firstLine = ticketText.split("\n")[0] ?? "";
  const title =
    shortTitle ||
    (firstLine.length > 80 ? firstLine.slice(0, 79) + "…" : firstLine) ||
    r.workflow_id ||
    r.id;
  return {
    id: r.id,
    ticket: { id: issue ? `#${issue}` : r.id.slice(0, 11), title },
    workflow: r.workflow_id ?? "—",
    status: r.status,
    cost: 0, // el coste por-run no viene en la lista; el drawer lo trae de /runs/{id}
    badges: [],
    createdAt: r.created_at ? new Date(r.created_at).toISOString() : undefined,
  };
}

// KernelStep es el shape de cada paso en GET /runs/{id} (step_id, type, status,
// result JSON-string). mapStep lo traduce al RunStep de la UI.
interface KernelStep {
  step_id: string;
  type: string;
  status: StepStatus;
  result?: string;
  error?: string;
}

function mapStep(s: KernelStep): RunStep {
  let detail = "";
  try {
    detail = String(JSON.parse(s.result ?? "{}")?.detail ?? "");
  } catch {
    /* result no-JSON */
  }
  return {
    id: s.step_id,
    kind: s.type,
    status: s.status,
    detail: s.error || detail || undefined,
  };
}

// KernelEvent es el shape del bus (REST y WS): seq, type, data JSON-string,
// created_at. mapEvent lo traduce al RunEvent renderizable de la UI. Exportado
// para que el cliente WS reuse exactamente la misma traducción.
export interface KernelEvent {
  seq: number;
  run_id?: string;
  task_id?: string;
  type: string;
  data?: string;
  created_at?: number;
}

export function mapEvent(e: KernelEvent): RunEvent {
  let data: Record<string, unknown> = {};
  try {
    data = JSON.parse(e.data ?? "{}") as Record<string, unknown>;
  } catch {
    /* data no-JSON */
  }
  const step = typeof data.step === "string" ? data.step : undefined;
  return {
    id: e.seq,
    runId: e.run_id ?? "",
    ts: e.created_at ? new Date(e.created_at).toLocaleTimeString() : "",
    type: e.type as RunEvent["type"],
    step,
    message: eventMessage(e.type, data, step),
    data,
  };
}

function eventMessage(type: string, data: Record<string, unknown>, step?: string): string {
  if (typeof data.detail === "string" && data.detail) return data.detail;
  const from = data.from;
  const to = data.to;
  if (from && to) return `${step ? step + ": " : ""}${from} → ${to}`;
  if (typeof data.workflow === "string") return `workflow: ${data.workflow}`;
  return type;
}

function mapRunDetail(r: KernelRun & { steps?: KernelStep[] }): RunDetail {
  // diff/costBreakdown/pr no vienen del kernel todavía → undefined (el drawer
  // los renderiza condicionalmente).
  return { ...mapRun(r), steps: (r.steps ?? []).map(mapStep) };
}

export async function getRun(id: string): Promise<RunDetail> {
  if (await isMock()) {
    const d = mockRunDetails[id];
    if (!d) throw new ApiError(404, `run ${id} no encontrado (mock)`);
    return d;
  }
  return mapRunDetail(await http<KernelRun & { steps?: KernelStep[] }>(`/runs/${id}`));
}

export async function getEvents(id: string, after = 0): Promise<RunEvent[]> {
  if (await isMock()) {
    return (mockEvents[id] || []).filter((e) => e.id > after);
  }
  const res = await http<KernelEvent[] | { events: KernelEvent[] }>(
    `/runs/${id}/events?after=${after}`
  );
  const raw = Array.isArray(res) ? res : res?.events ?? [];
  return raw.map(mapEvent);
}

export async function getStats(): Promise<BoardStats> {
  if (await isMock()) return mockStats;
  // GET /metrics trae el desglose; lo mapeamos al resumen del board. by_status
  // cuenta runs por estado. openPRs no está en el contrato del kernel todavía.
  const m = await http<MetricsPayload>(`/metrics`);
  const bs = m.by_status ?? {};
  return {
    running: bs.RUNNING ?? 0,
    awaiting: bs.AWAITING ?? 0,
    openPRs: bs.DONE ?? 0,
    projectCost: m.total_cost_usd ?? 0,
  };
}

export interface SpendToday {
  cost: number;
  tokens: string;
}

export async function getSpendToday(): Promise<SpendToday> {
  if (await isMock()) return mockSpendToday;
  // GET /metrics → extraemos total_cost_usd y lo mapeamos a SpendToday.
  // Los tokens no están en el contrato actual, así que estimamos de by_step si llegan,
  // o mostramos "—" para no inventar un valor.
  const m = await http<MetricsPayload>(`/metrics`);
  return { cost: m.total_cost_usd, tokens: "—" };
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
  return mapRun(await http<KernelRun>(`/runs`, { method: "POST", body: JSON.stringify(input) }));
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

// ─────────────────────────────────────────────────────────────────────────────
// Registry — GET/PUT/DELETE /registry/{kind}/{id}, GET /registry/{kind}
// ─────────────────────────────────────────────────────────────────────────────

export async function listRegistry(kind: RegistryKind): Promise<RegistryListResponse> {
  if (await isMock()) return { ids: mockRegistryIds[kind] ?? [] };
  return http<RegistryListResponse>(`/registry/${kind}`);
}

/** Devuelve el contenido crudo (YAML para agents/workflows, markdown para skills). */
export async function getRegistryItem(kind: RegistryKind, id: string): Promise<string> {
  if (await isMock()) {
    const content = mockRegistryContent[kind]?.[id];
    if (!content) throw new ApiError(404, `${kind}/${id} no encontrado (mock)`);
    return content;
  }
  // La API devuelve texto crudo (application/yaml o text/markdown); no parseamos JSON.
  const res = await fetch(`${API_URL}/registry/${kind}/${id}`);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `GET /registry/${kind}/${id} → ${res.status} ${body}`);
  }
  return res.text();
}

export async function saveRegistryItem(kind: RegistryKind, id: string, body: string): Promise<RegistrySaveResponse> {
  if (await isMock()) {
    // Actualizar el mock en memoria para que la UI refleje el cambio.
    if (!mockRegistryContent[kind]) mockRegistryContent[kind] = {};
    mockRegistryContent[kind][id] = body;
    if (!mockRegistryIds[kind].includes(id)) mockRegistryIds[kind].push(id);
    return { saved: id };
  }
  const res = await fetch(`${API_URL}/registry/${kind}/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/yaml" },
    body,
  });
  if (!res.ok) {
    const errBody = await res.text().catch(() => "");
    throw new ApiError(res.status, errBody || `PUT /registry/${kind}/${id} → ${res.status}`);
  }
  return (await res.json()) as RegistrySaveResponse;
}

/** Persona sidecar para un agente: GET /registry/agents/{id}/persona.
 *  Devuelve el markdown de <id>.md; cadena vacía si el sidecar no existe (404). */
export async function getAgentPersona(id: string): Promise<string> {
  if (await isMock()) {
    // En modo mock no hay sidecars — devolvemos vacío para no bloquear la vista.
    return "";
  }
  const res = await fetch(`${API_URL}/registry/agents/${encodeURIComponent(id)}/persona`);
  if (res.status === 404) return "";
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `GET /registry/agents/${id}/persona → ${res.status} ${body}`);
  }
  return res.text();
}

export async function deleteRegistryItem(kind: RegistryKind, id: string): Promise<RegistryDeleteResponse> {
  if (await isMock()) {
    const idx = mockRegistryIds[kind]?.indexOf(id) ?? -1;
    if (idx >= 0) mockRegistryIds[kind].splice(idx, 1);
    if (mockRegistryContent[kind]) delete mockRegistryContent[kind][id];
    return { deleted: id };
  }
  return http<RegistryDeleteResponse>(`/registry/${kind}/${id}`, { method: "DELETE" });
}

// ─────────────────────────────────────────────────────────────────────────────
// Settings — GET/PUT /settings
// ─────────────────────────────────────────────────────────────────────────────

// El kernel guarda mcp como mapa {nombre: {...}} y merge_policy con claves
// low/high; la UI usa una lista de conexiones y low_risk/high_risk. Estos
// adapters traducen en ambos sentidos sin romper el contrato del backend.
interface RawSettings {
  mcp?: Record<string, Record<string, unknown>>;
  agent_auth?: { mode?: string; secret?: string };
  sandbox?: { runtime?: string; image?: string; egress?: string };
  merge_policy?: Record<string, string>;
}

function settingsFromBackend(r: RawSettings): SettingsPayload {
  const mcp: McpConnection[] = Object.entries(r.mcp ?? {}).map(([name, v]) => ({
    name,
    url: String(v?.repo ?? v?.url ?? ""),
    token: String(v?.token ?? "••••••••"),
  }));
  return {
    mcp,
    agent_auth: { mode: r.agent_auth?.mode ?? "subscription", secret: r.agent_auth?.secret ?? "" },
    sandbox: { runtime: r.sandbox?.runtime ?? "", image: r.sandbox?.image ?? "" },
    merge_policy: { low_risk: r.merge_policy?.low ?? "", high_risk: r.merge_policy?.high ?? "" },
  };
}

function settingsToBackend(s: SettingsPayload): RawSettings {
  const mcp: Record<string, Record<string, unknown>> = {};
  for (const c of s.mcp ?? []) mcp[c.name] = { url: c.url, token: c.token };
  return {
    mcp,
    agent_auth: s.agent_auth,
    sandbox: { runtime: s.sandbox.runtime, image: s.sandbox.image },
    merge_policy: { low: s.merge_policy.low_risk, high: s.merge_policy.high_risk },
  };
}

export async function getSettings(): Promise<SettingsPayload> {
  if (await isMock()) return { ...mockSettings };
  return settingsFromBackend(await http<RawSettings>(`/settings`));
}

export async function saveSettings(payload: SettingsPayload): Promise<SettingsPayload> {
  if (await isMock()) {
    Object.assign(mockSettings, payload);
    return { ...mockSettings };
  }
  const saved = await http<RawSettings>(`/settings`, {
    method: "PUT",
    body: JSON.stringify(settingsToBackend(payload)),
  });
  return settingsFromBackend(saved);
}

// ─────────────────────────────────────────────────────────────────────────────
// Metrics — GET /metrics (control-plane)
// ─────────────────────────────────────────────────────────────────────────────

export async function getMetrics(): Promise<MetricsPayload> {
  if (await isMock()) return { ...mockMetrics };
  return http<MetricsPayload>(`/metrics`);
}

// ─────────────────────────────────────────────────────────────────────────────
// Tickets — GET /tickets del STORE NATIVO (control-plane, API_URL). El orquestador
// y GitHub pasan a ser sync opcional (B2); la UI lee del store propio.
// ─────────────────────────────────────────────────────────────────────────────

export async function listTickets(): Promise<OrchestratorTicket[]> {
  // Fuente de verdad = el store NATIVO del control-plane (GET /tickets en :8080),
  // no el orquestador. Así la UI es self-contained: lee epics/stories/deps del
  // store propio. El orquestador/GitHub pasan a ser sync opcional (B2).
  if (await isMock()) return [...mockOrchestratorTickets];
  const res = await fetch(`${API_URL}/tickets`);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `GET /tickets → ${res.status} ${body}`);
  }
  // Toleramos array pelado además de { tickets: [...] }, espejando el patrón de listRuns.
  const json = await res.json();
  return Array.isArray(json) ? json : (json as { tickets?: OrchestratorTicket[] })?.tickets ?? [];
}

// ─────────────────────────────────────────────────────────────────────────────
// Epics — GET /epics (opcional; selector en el formulario de nueva story)
// ─────────────────────────────────────────────────────────────────────────────

export async function listEpics(): Promise<Epic[]> {
  if (await isMock()) return [...mockEpics];
  const res = await fetch(`${API_URL}/epics`);
  if (!res.ok) return []; // el endpoint es opcional; fallamos silenciosamente
  const json = await res.json();
  return Array.isArray(json) ? json : (json as { epics?: Epic[] })?.epics ?? [];
}

// ─────────────────────────────────────────────────────────────────────────────
// Stories — POST /stories (crea una story nueva en el store nativo)
// ─────────────────────────────────────────────────────────────────────────────

export interface CreateStoryInput {
  id: string;
  title: string;
  deps?: string[];
  epic_id?: string;
  sprint_id?: string;
}

export async function createStory(input: CreateStoryInput): Promise<OrchestratorTicket> {
  if (await isMock()) {
    // Verificar id duplicado en el mock
    const exists = mockOrchestratorTickets.find((t) => t.id === input.id);
    if (exists) throw new ApiError(400, `Story con id "${input.id}" ya existe.`);
    const story: OrchestratorTicket = {
      id: input.id,
      title: input.title,
      status: "backlog",
      deps: input.deps ?? [],
    };
    mockOrchestratorTickets.push(story);
    return story;
  }
  return http<OrchestratorTicket>(`/stories`, {
    method: "POST",
    body: JSON.stringify({ ...input, status: "backlog" }),
  });
}

// ─────────────────────────────────────────────────────────────────────────────
// Projects — POST /projects, GET /projects
// Un proyecto es un repositorio de GitHub. Crear un proyecto = crear el repo.
// ─────────────────────────────────────────────────────────────────────────────

/** Crea un proyecto (y su repo en GitHub). Devuelve el proyecto con la URL del repo. */
export async function createProject(name: string, description: string): Promise<Project> {
  if (await isMock()) {
    // Verificar nombre duplicado en el mock.
    const exists = mockProjects.find((p) => p.name === name);
    if (exists) throw new ApiError(409, `El repositorio "${name}" ya existe.`);
    const proj: Project = {
      id: `proj_${String(mockProjects.length + 1).padStart(3, "0")}`,
      name,
      description,
      repo: `https://github.com/vibeforge-demo/${name}`,
    };
    mockProjects.push(proj);
    return proj;
  }
  return http<Project>(`/projects`, {
    method: "POST",
    body: JSON.stringify({ name, description }),
  });
}

/** Lista todos los proyectos del control-plane. */
export async function listProjects(): Promise<Project[]> {
  if (await isMock()) return [...mockProjects];
  const res = await http<Project[] | { projects: Project[] }>(`/projects`);
  return Array.isArray(res) ? res : res?.projects ?? [];
}

// ─────────────────────────────────────────────────────────────────────────────
// Studio — Design runs
// Mapea runs del workflow "design" al modelo DesignRun. Las fases se derivan
// de los steps con el mapa hardcodeado de la especificación.
// ─────────────────────────────────────────────────────────────────────────────

// Mapa de fases del workflow "design": stepId del agente → nombre display.
// El gate de cada fase NO siempre es "<stepId>_gate" (architecture → arch_gate),
// así que el id del gate es explícito. handoff no tiene gate.
const DESIGN_PHASE_MAP: { stepId: string; name: string; gateId: string }[] = [
  { stepId: "discovery", name: "Descubrimiento", gateId: "discovery_gate" },
  { stepId: "prd", name: "PRD", gateId: "prd_gate" },
  { stepId: "architecture", name: "Arquitectura", gateId: "arch_gate" },
  { stepId: "ui", name: "UI / Pantallas", gateId: "ui_gate" },
  { stepId: "backlog", name: "Backlog", gateId: "backlog_gate" },
  { stepId: "handoff", name: "Handoff → stories", gateId: "" },
];

interface KernelRunWithSteps extends KernelRun {
  steps?: KernelStep[];
}

function mapDesignRun(r: KernelRunWithSteps): DesignRun {
  let idea = "";
  let project_id: string | undefined;
  let repo: string | undefined;
  try {
    const p = JSON.parse(r.payload ?? "{}");
    idea = typeof p.instructions === "string" ? p.instructions : "";
    project_id = typeof p.project_id === "string" ? p.project_id : undefined;
    repo = typeof p.repo === "string" ? p.repo : undefined;
  } catch {
    /* payload no-JSON */
  }

  // Construir fases derivando el estado de los steps cuando están disponibles.
  const steps = r.steps ?? [];
  const statusOf = (sid: string): DesignStepStatus => {
    const s = steps.find((st) => st.step_id === sid);
    return (s?.status ?? "QUEUED") as DesignStepStatus;
  };

  const phases: DesignPhase[] = DESIGN_PHASE_MAP.map(({ stepId, name, gateId }) => ({
    stepId,
    name,
    gateId,
    designStatus: statusOf(stepId),
    gateStatus: gateId ? statusOf(gateId) : "QUEUED",
  }));

  return {
    id: r.id,
    workflow_id: r.workflow_id ?? "design",
    status: r.status,
    idea,
    created_at: r.created_at ? r.created_at * 1000 : Date.now(),
    phases,
    project_id,
    repo,
  };
}

/** Lista los runs del workflow "design" (filtrando client-side). */
export async function listDesignRuns(): Promise<DesignRun[]> {
  if (await isMock()) return [...mockDesignRuns];
  const res = await http<KernelRun[] | { runs: KernelRun[] }>(`/runs`);
  const raw = Array.isArray(res) ? res : res?.runs ?? [];
  return raw
    .filter((r) => r.workflow_id === "design")
    .map((r) => mapDesignRun(r));
}

export interface CreateDesignRunInput {
  project_id: string;
  repo: string;
  instructions: string;
}

/** Crea un run de diseño vinculado a un proyecto/repo de GitHub. */
export async function createDesignRun(input: CreateDesignRunInput): Promise<DesignRun> {
  if (await isMock()) {
    const newRun: DesignRun = {
      id: `run_design_${String(mockDesignRuns.length + 1).padStart(3, "0")}`,
      workflow_id: "design",
      status: "QUEUED",
      idea: input.instructions,
      created_at: Date.now(),
      project_id: input.project_id,
      repo: input.repo,
      phases: DESIGN_PHASE_MAP.map(({ stepId, name, gateId }) => ({
        stepId,
        name,
        gateId,
        designStatus: "QUEUED" as DesignStepStatus,
        gateStatus: "QUEUED" as DesignStepStatus,
      })),
    };
    mockDesignRuns.unshift(newRun);
    return newRun;
  }
  const r = await http<KernelRun>(`/runs`, {
    method: "POST",
    body: JSON.stringify({
      workflow: "design",
      payload: {
        project_id: input.project_id,
        repo: input.repo,
        instructions: input.instructions,
      },
    }),
  });
  return mapDesignRun(r);
}

/** Detalle de un design run con fases actualizadas desde los steps. */
export async function getDesignRun(id: string): Promise<DesignRun> {
  if (await isMock()) {
    const run = mockDesignRuns.find((r) => r.id === id);
    if (!run) throw new ApiError(404, `design run ${id} no encontrado (mock)`);
    return { ...run };
  }
  const r = await http<KernelRunWithSteps>(`/runs/${id}`);
  return mapDesignRun(r);
}

/** Trae el artefacto (documento markdown) producido por un paso de diseño. */
export async function getArtifact(runId: string, stepId: string): Promise<string> {
  if (await isMock()) {
    const text = mockArtifacts[runId]?.[stepId];
    if (!text) throw new ApiError(404, `artifact ${runId}/${stepId} no encontrado (mock)`);
    return text;
  }
  // El result del step trae el doc en result.output.text (el map Output del
  // runner) o en result.detail; result.text no existe. Probamos en ese orden.
  const res = await http<{
    run?: unknown;
    kind?: string;
    result?: { text?: string; detail?: string; output?: { text?: string } };
  }>(`/runs/${runId}/artifacts/${stepId}`);
  const r = res?.result;
  return r?.output?.text ?? r?.detail ?? r?.text ?? "";
}
