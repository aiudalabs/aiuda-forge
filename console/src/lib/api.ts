// Capa única de cliente de API. Lee VIBEFORGE_API_URL (lib/config) y habla con el control-plane
// del kernel v2 por HTTP. Toda acción de UI = un endpoint del contrato (doc 14). Ninguna lógica
// de negocio del flujo vive aquí: solo fetch + tipos.
//
// MODO MOCK: si la API no responde (o NEXT_PUBLIC_FORCE_MOCK=1) caemos a datos de ejemplo
// (lib/mock) para que la UI se construya/vea sin el backend arriba. `getApiMode()` expone el
// modo activo para que la UI lo muestre y para que el WS sepa si conectarse.

import { API_URL, FORCE_MOCK, HEALTH_TIMEOUT_MS } from "./config";
import { authHeaders, handleUnauthorized } from "./auth";
import {
  DEFAULT_PROJECT_SETTINGS,
  MOCK_PROJECT,
  mockArtifacts,
  mockControl,
  mockDesignRuns,
  mockEpics,
  mockEvents,
  mockMetrics,
  mockNotifications,
  mockOrchestratorTickets,
  mockProjectSettings,
  mockProjects,
  mockRegistryContent,
  mockRegistryIds,
  mockRunDetails,
  mockRuns,
  mockSettings,
  mockSpendToday,
  mockStats,
  mockTicketsByProject,
} from "./mock";
import type {
  ExecutorInfo,
  BoardStats,
  ControlStatus,
  DesignPhase,
  DesignRun,
  DesignStepStatus,
  DispatchCandidates,
  DispatchResult,
  GitHubSpend,
  McpConnection,
  MetricsPayload,
  Notification,
  OrchestratorTicket,
  Project,
  ProjectSettings,
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
  Member,
  Invite,
  MembersPayload,
  Role,
  ProjectPR,
  ApproveWorkflowResult,
} from "./types";

export type ApiMode = "real" | "mock";

let modePromise: Promise<ApiMode> | null = null;

async function probe(): Promise<ApiMode> {
  // Mock is OPT-IN ONLY (NEXT_PUBLIC_FORCE_MOCK=1). There is NO silent fallback to
  // mock when the backend is unreachable: showing fake demo data that looks real is
  // dangerous and confusing (it once masked a real project as a set of example
  // tickets). If the API is down we STAY in "real" mode and surface real errors.
  if (FORCE_MOCK) return "mock";
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), HEALTH_TIMEOUT_MS);
    const res = await fetch(`${API_URL}/healthz`, { signal: ctrl.signal });
    clearTimeout(t);
    if (!res.ok) console.warn(`API /healthz returned ${res.status} — staying in real mode (no mock fallback)`);
  } catch (e) {
    console.warn("API unreachable — staying in real mode (no mock fallback):", e);
  }
  return "real";
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
    // El token Bearer va en cada llamada (audit C1). authHeaders() es {} cuando
    // no hay token (modo loopback abierto), así que no rompe el dev sin auth.
    headers: { "Content-Type": "application/json", ...authHeaders(), ...(init?.headers || {}) },
  });
  if (res.status === 401) {
    handleUnauthorized(); // borra token + redirige a /login
    throw new ApiError(401, `${init?.method || "GET"} ${path} → 401 unauthorized`);
  }
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

// rawFetch envuelve fetch para las llamadas que devuelven texto crudo (registry
// YAML/markdown) o JSON no-mapeado: añade el header Bearer (audit C1) y maneja el
// 401 (borra token + redirige a /login). Las cabeceras extra (p.ej.
// Content-Type: application/yaml en PUT) se mezclan encima.
async function rawFetch(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: { ...authHeaders(), ...(init?.headers || {}) },
  });
  if (res.status === 401) {
    handleUnauthorized();
    throw new ApiError(401, `${init?.method || "GET"} ${path} → 401 unauthorized`);
  }
  return res;
}

// ─────────────────────────────────────────────────────────────────────────────
// Lectura
// ─────────────────────────────────────────────────────────────────────────────

export async function listRuns(params?: { status?: RunStatus; project?: string }): Promise<Run[]> {
  if (await isMock()) {
    let runs = [...mockRuns];
    // Scope multi-tenant (Wave 2): el board/runs cuelga de un proyecto. En real el
    // backend filtra con ?project=; aquí espejamos filtrando por el campo project.
    if (params?.project) runs = runs.filter((r) => r.project === params.project);
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
  // step.event = una acción fina del agente (lo que claude HIZO): tool_use / text.
  // Se renderiza como una línea de log estilo terminal: "→ Edit notas.py", etc.
  if (type === "step.event") return stepEventLine(data);
  if (typeof data.detail === "string" && data.detail) return data.detail;
  const from = data.from;
  const to = data.to;
  if (from && to) return `${step ? step + ": " : ""}${from} → ${to}`;
  if (typeof data.workflow === "string") return `workflow: ${data.workflow}`;
  return type;
}

// stepEventLine formatea un evento fino del agente (payload del engine:
// {kind:"tool_use",tool,input} | {kind:"text",text} | {kind:"system",subtype})
// como una sola línea legible para el live-log.
function stepEventLine(data: Record<string, unknown>): string {
  const kind = typeof data.kind === "string" ? data.kind : "";
  if (kind === "tool_use") {
    const tool = typeof data.tool === "string" ? data.tool : "tool";
    const input = typeof data.input === "string" ? data.input : "";
    const arg = toolArg(tool, input);
    return arg ? `→ ${tool}: ${arg}` : `→ ${tool}`;
  }
  if (kind === "tool_result") {
    const tool = typeof data.tool === "string" ? data.tool : "tool";
    const output = typeof data.output === "string" ? data.output : "";
    return `← ${tool}: ${firstLine(output)}`;
  }
  if (kind === "thinking") {
    const text = typeof data.text === "string" ? data.text : "";
    return `💭 ${truncate(text, 200)}`;
  }
  if (kind === "text") {
    const text = typeof data.text === "string" ? data.text : "";
    return firstNLines(text, 3, 300);
  }
  if (kind === "system") {
    const sub = typeof data.subtype === "string" ? data.subtype : "";
    return sub ? `system: ${sub}` : "system";
  }
  return "step.event";
}

// toolArg saca el dato más relevante del input del tool para la línea de log:
// el path para Read/Edit/Write, el comando para Bash, el patrón para Grep/Glob.
// El input llega como JSON (truncado en el engine) o como string plano.
function toolArg(tool: string, input: string): string {
  if (!input) return "";
  let parsed: Record<string, unknown> | null = null;
  try {
    const p = JSON.parse(input);
    if (p && typeof p === "object") parsed = p as Record<string, unknown>;
  } catch {
    return firstLine(input);
  }
  if (!parsed) return firstLine(input);
  const pick = (k: string) => (typeof parsed![k] === "string" ? (parsed![k] as string) : "");
  const t = tool.toLowerCase();
  if (t === "bash") return firstLine(pick("command"));
  if (t === "read" || t === "edit" || t === "write" || t === "multiedit")
    return shortPath(pick("file_path") || pick("path"));
  if (t === "grep" || t === "glob") return pick("pattern") || pick("query");
  // Fallback: primer string del objeto, o el JSON compacto.
  for (const v of Object.values(parsed)) {
    if (typeof v === "string" && v) return firstLine(v);
  }
  return firstLine(input);
}

function firstLine(s: string): string {
  const line = s.split("\n", 1)[0].trim();
  return line.length > 160 ? line.slice(0, 160) + "…" : line;
}

// firstNLines toma las primeras n líneas, las une con un espacio y trunca a
// maxChars — para mostrar un vistazo multilínea del texto del agente en el log.
function firstNLines(s: string, n: number, maxChars: number): string {
  const joined = s.split("\n").slice(0, n).join(" ").trim();
  return joined.length > maxChars ? joined.slice(0, maxChars) + "…" : joined;
}

function truncate(s: string, maxChars: number): string {
  const t = s.replace(/\s+/g, " ").trim();
  return t.length > maxChars ? t.slice(0, maxChars) + "…" : t;
}

// shortPath recorta paths absolutos largos a las últimas 2 componentes para que
// la línea lea "dir/notas.py" en vez del path completo del workdir del run.
function shortPath(p: string): string {
  if (!p) return "";
  const parts = p.split("/").filter(Boolean);
  return parts.length <= 2 ? p : parts.slice(-2).join("/");
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

// project= es un query param ADITIVO: el contrato no lista /metrics como scopeado,
// así que lo pasamos solo cuando hay proyecto activo y el engine puede ignorarlo
// sin romper. Así las stat-cards reflejan el proyecto activo si el backend lo soporta.
function metricsPath(project?: string): string {
  return project ? `/metrics?project=${encodeURIComponent(project)}` : `/metrics`;
}

export async function getStats(project?: string): Promise<BoardStats> {
  if (await isMock()) return mockStats;
  // GET /metrics trae el desglose; lo mapeamos al resumen del board. by_status
  // cuenta runs por estado. openPRs no está en el contrato del kernel todavía.
  const m = await http<MetricsPayload>(metricsPath(project));
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

export async function getSpendToday(project?: string): Promise<SpendToday> {
  if (await isMock()) return mockSpendToday;
  // GET /metrics → extraemos total_cost_usd y lo mapeamos a SpendToday.
  // Los tokens no están en el contrato actual, así que estimamos de by_step si llegan,
  // o mostramos "—" para no inventar un valor.
  const m = await http<MetricsPayload>(metricsPath(project));
  return { cost: m.total_cost_usd, tokens: "—" };
}

export async function getControlStatus(): Promise<ControlStatus> {
  if (await isMock()) return mockControl;
  return http<ControlStatus>(`/control/status`);
}

export async function getNotifications(project?: string): Promise<Notification[]> {
  // Derivadas de runs en AWAITING/FAILED — el kernel no expone /notifications todavía, así que
  // la campana refleja el estado vivo del board (mock o real). Scopeada al proyecto activo
  // (Wave 2). TODO(endpoint): GET /notifications o derivar del stream del bus.
  const runs = await listRuns({ project });
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

// Sprints (the planned increments, with name + goal). Used by the Sprints backlog
// view; scoped to the active project client-side via the project's tickets.
export async function listSprints(): Promise<import("./types").Sprint[]> {
  if (await isMock()) return [];
  const res = await http<{ sprints: import("./types").Sprint[] }>(`/sprints`);
  return res.sprints ?? [];
}

// Project docs (Studio = Confluence, U1): read the project's specs straight from
// its repo docs/ tree — the persistent source of truth.
export async function listProjectDocs(projectId: string, ref = "dev"): Promise<import("./types").DocEntry[]> {
  if (await isMock()) return [];
  const res = await http<{ docs: import("./types").DocEntry[] }>(
    `/projects/${projectId}/docs?ref=${encodeURIComponent(ref)}`,
  );
  return res.docs ?? [];
}

export async function getProjectDoc(projectId: string, path: string, ref = "dev"): Promise<string> {
  if (await isMock()) return "";
  const res = await http<{ content: string }>(
    `/projects/${projectId}/docs/file?path=${encodeURIComponent(path)}&ref=${encodeURIComponent(ref)}`,
  );
  return res.content ?? "";
}

// Requeue (R2): resurrect a failed run's stories back to backlog so the
// orchestrator re-fires them. In sprint mode this requeues the WHOLE sprint.
// Requeue de UNA story (pivote F2): espejada → backlog + limpia su sesión de
// agente; legacy → transición guardada failed→backlog del kernel.
export async function requeueStory(id: string): Promise<void> {
  await http(`/stories/${encodeURIComponent(id)}/requeue`, { method: "POST" });
}

export async function requeueRun(id: string): Promise<number> {
  if (await isMock()) {
    mutateMockStatus(id, "QUEUED");
    return 0;
  }
  const res = await http<{ requeued: number }>(`/runs/${id}/requeue`, { method: "POST" });
  return res.requeued;
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
  const res = await rawFetch(`/registry/${kind}/${id}`);
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
  const res = await rawFetch(`/registry/${kind}/${id}`, {
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
  const res = await rawFetch(`/registry/agents/${encodeURIComponent(id)}/persona`);
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

// El kernel guarda mcp como mapa {nombre: {...}}; la UI usa una lista de conexiones.
// Estos adapters traducen en ambos sentidos sin romper el contrato del backend.
// Wave 2: el GLOBAL settings perdió merge_policy, execution_unit y merge_mode — esos
// son ahora per-proyecto (ver getProjectSettings). Aquí solo mcp · agent_auth · sandbox.
interface RawSettings {
  mcp?: Record<string, Record<string, unknown>>;
  agent_auth?: { mode?: string; secret?: string };
  sandbox?: { runtime?: string; image?: string; egress?: string };
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
  };
}

function settingsToBackend(s: SettingsPayload): RawSettings {
  const mcp: Record<string, Record<string, unknown>> = {};
  for (const c of s.mcp ?? []) mcp[c.name] = { url: c.url, token: c.token };
  return {
    mcp,
    agent_auth: s.agent_auth,
    sandbox: { runtime: s.sandbox.runtime, image: s.sandbox.image },
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
// Per-project settings — GET/PUT /projects/{id}/settings (Wave 2)
// execution_unit + merge_mode viven por proyecto. El payload del backend es plano
// ({execution_unit, merge_mode}) — sin adapter de shape, solo defaults defensivos.
// ─────────────────────────────────────────────────────────────────────────────

function projectSettingsFrom(r: Partial<ProjectSettings>): ProjectSettings {
  return {
    execution_unit: r.execution_unit === "story" ? "story" : "sprint",
    merge_mode: r.merge_mode === "auto" ? "auto" : "manual",
    dispatch_mode:
      r.dispatch_mode === "auto" || r.dispatch_mode === "off" ? r.dispatch_mode : "approve",
    executor: r.executor === "claude_action" ? "claude_action" : "copilot",
    model_by_lane: r.model_by_lane ?? {},
    workflow_approval: r.workflow_approval === "auto_if_safe" ? "auto_if_safe" : "manual",
    max_concurrency:
      typeof r.max_concurrency === "number" && r.max_concurrency > 0
        ? Math.floor(r.max_concurrency)
        : 0,
  };
}

export async function getProjectSettings(projectId: string): Promise<ProjectSettings> {
  if (await isMock()) {
    return { ...(mockProjectSettings[projectId] ?? DEFAULT_PROJECT_SETTINGS) };
  }
  return projectSettingsFrom(await http<Partial<ProjectSettings>>(`/projects/${projectId}/settings`));
}

export async function saveProjectSettings(
  projectId: string,
  payload: ProjectSettings,
): Promise<ProjectSettings> {
  if (await isMock()) {
    mockProjectSettings[projectId] = { ...payload };
    return { ...payload };
  }
  const saved = await http<Partial<ProjectSettings>>(`/projects/${projectId}/settings`, {
    method: "PUT",
    body: JSON.stringify(payload),
  });
  return projectSettingsFrom(saved);
}

// ─────────────────────────────────────────────────────────────────────────────
// Dispatch (pivote F2) — el ready-set del conductor y el despacho a agentes de
// GitHub. GET candidates (miembro+) · POST dispatch (editor+, 409 si la unidad
// dejó de ser candidata — recargar y reintentar).
// ─────────────────────────────────────────────────────────────────────────────

export async function getDispatchCandidates(projectId: string): Promise<DispatchCandidates> {
  if (await isMock()) {
    return { execution_unit: "sprint", candidates: [] };
  }
  return http<DispatchCandidates>(`/projects/${projectId}/dispatch/candidates`);
}

export async function dispatchWork(
  projectId: string,
  unit: { story_id?: string; sprint_id?: string; executor?: string },
): Promise<DispatchResult> {
  if (await isMock()) {
    return { dispatched: [unit.story_id ?? unit.sprint_id ?? ""], channel: unit.executor ?? "copilot", model: "" };
  }
  return http<DispatchResult>(`/projects/${projectId}/dispatch`, {
    method: "POST",
    body: JSON.stringify(unit),
  });
}

/** Canales de ejecución realmente disponibles en el GitHub del proyecto. */
export async function getExecutors(projectId: string): Promise<ExecutorInfo[]> {
  if (await isMock()) {
    return [
      { id: "copilot", available: true, default: true },
      { id: "claude_action", available: true, default: false },
    ];
  }
  const r = await http<{ executors: ExecutorInfo[] }>(`/projects/${projectId}/executors`);
  return r.executors;
}

// ─────────────────────────────────────────────────────────────────────────────
// Metrics — GET /metrics (control-plane)
// ─────────────────────────────────────────────────────────────────────────────

export async function getMetrics(project?: string): Promise<MetricsPayload> {
  if (await isMock()) return { ...mockMetrics };
  return http<MetricsPayload>(metricsPath(project));
}

// ─────────────────────────────────────────────────────────────────────────────
// Spend desde GitHub (F4) — GET /projects/{id}/spend/github. Gasto medido del
// repo (Copilot/Actions/LFS) del ciclo actual. Degrada a { available:false } si
// GitHub no expone la facturación del owner del repo (no es un error del sistema).
// ─────────────────────────────────────────────────────────────────────────────

export async function getGitHubSpend(projectId: string): Promise<GitHubSpend> {
  if (await isMock()) return { available: false, reason: "modo mock" };
  return http<GitHubSpend>(`/projects/${projectId}/spend/github`);
}

// ─────────────────────────────────────────────────────────────────────────────
// Tickets — GET /tickets del STORE NATIVO (control-plane, API_URL). El orquestador
// y GitHub pasan a ser sync opcional (B2); la UI lee del store propio.
// ─────────────────────────────────────────────────────────────────────────────

export async function listTickets(project?: string): Promise<OrchestratorTicket[]> {
  // Fuente de verdad = el store NATIVO del control-plane (GET /tickets en :8080),
  // no el orquestador. Así la UI es self-contained: lee epics/stories/deps del
  // store propio. Scopeada al proyecto activo (Wave 2): GET /tickets?project=<id>.
  if (await isMock()) {
    if (project) return [...(mockTicketsByProject[project] ?? [])];
    return [...mockTicketsByProject[MOCK_PROJECT]];
  }
  const qs = project ? `?project=${encodeURIComponent(project)}` : "";
  const res = await rawFetch(`/tickets${qs}`);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `GET /tickets → ${res.status} ${body}`);
  }
  // Toleramos array pelado además de { tickets: [...] }, espejando el patrón de listRuns.
  const json = await res.json();
  return Array.isArray(json) ? json : (json as { tickets?: OrchestratorTicket[] })?.tickets ?? [];
}

// ─────────────────────────────────────────────────────────────────────────────
// Sprints listos — GET /sprints/ready?project=<id> (contrato Wave 2)
// Sprints cuyas deps están resueltas y pueden ejecutarse. El orquestador los toma.
// La UI aún no tiene una vista dedicada; la función existe para cerrar el contrato y
// mockearse. En mock derivamos "ready" de los tickets ready del proyecto.
// ─────────────────────────────────────────────────────────────────────────────

export async function readySprints(project?: string): Promise<OrchestratorTicket[]> {
  if (await isMock()) {
    const list = project ? (mockTicketsByProject[project] ?? []) : mockTicketsByProject[MOCK_PROJECT];
    return list.filter((t) => t.status === "ready");
  }
  const qs = project ? `?project=${encodeURIComponent(project)}` : "";
  const res = await rawFetch(`/sprints/ready${qs}`);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `GET /sprints/ready → ${res.status} ${body}`);
  }
  const json = await res.json();
  return Array.isArray(json) ? json : (json as { sprints?: OrchestratorTicket[] })?.sprints ?? [];
}

// ─────────────────────────────────────────────────────────────────────────────
// Epics — GET /epics (opcional; selector en el formulario de nueva story)
// ─────────────────────────────────────────────────────────────────────────────

export async function listEpics(): Promise<Epic[]> {
  if (await isMock()) return [...mockEpics];
  const res = await rawFetch(`/epics`);
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
  /** Descripción tipo user-story — el campo `body` que ya pintan la card y el drawer. */
  body?: string;
  deps?: string[];
  epic_id?: string;
  sprint_id?: string;
  /** Proyecto dueño. Sin él, el backend manda la story al proyecto "default" y
   *  nunca aparece en el board scopeado. */
  project_id: string;
}

export async function createStory(input: CreateStoryInput): Promise<OrchestratorTicket> {
  if (await isMock()) {
    // Verificar id duplicado en el mock
    const exists = mockOrchestratorTickets.find((t) => t.id === input.id);
    if (exists) throw new ApiError(400, `Story con id "${input.id}" ya existe.`);
    const story: OrchestratorTicket = {
      id: input.id,
      title: input.title,
      body: input.body,
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
  { stepId: "mockups", name: "Mockups", gateId: "mockups_gate" },
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
    // Use the LATEST instance of the step (#18). A retry leaves an older FAILED
    // task plus a newer DONE; steps arrive created_at ASC, so find() (first match)
    // would report the stale FAILED and the artifact would never render even
    // though the phase re-completed. Keep the last match = the current attempt.
    let s: (typeof steps)[number] | undefined;
    for (const st of steps) {
      if (st.step_id === sid) s = st;
    }
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

// ─────────────────────────────────────────────────────────────────────────────
// Members & invitations (v1.2 roles)
// ─────────────────────────────────────────────────────────────────────────────

/** GET /projects/{id}/members — miembros (email resuelto) + invites pendientes. */
export async function listMembers(projectId: string): Promise<MembersPayload> {
  const res = await http<MembersPayload>(`/projects/${projectId}/members`);
  return { members: res.members ?? [], invites: res.invites ?? [] };
}

export interface InviteResult {
  status: "added" | "invited";
  email: string;
  role: Role;
  user_id?: string;
  token?: string;
  invite_path?: string;
}

/** POST /projects/{id}/members — invita por email+rol (owner-only en el backend). */
export async function inviteMember(projectId: string, email: string, role: Role): Promise<InviteResult> {
  return http<InviteResult>(`/projects/${projectId}/members`, {
    method: "POST",
    body: JSON.stringify({ email, role }),
  });
}

/** PUT /projects/{id}/members/{userId} — cambia el rol de un miembro. */
export async function updateMemberRole(projectId: string, userId: string, role: Role): Promise<void> {
  await http<unknown>(`/projects/${projectId}/members/${userId}`, {
    method: "PUT",
    body: JSON.stringify({ role }),
  });
}

/** DELETE /projects/{id}/members/{userId} — quita un miembro. */
export async function removeMember(projectId: string, userId: string): Promise<void> {
  await http<unknown>(`/projects/${projectId}/members/${userId}`, { method: "DELETE" });
}

/** POST /invites/{token}/accept — el usuario invitado acepta y se une al proyecto. */
export async function acceptInvite(token: string): Promise<{ project_id: string; role: Role }> {
  return http<{ project_id: string; role: Role }>(`/invites/${token}/accept`, { method: "POST" });
}

// Member/Invite re-exported through types; referenced here to satisfy the imports.
export type { Member, Invite };

// ─────────────────────────────────────────────────────────────────────────────
// Channels & import (v1.3)
// ─────────────────────────────────────────────────────────────────────────────

import type { Channel } from "./types";

/** GET /projects/{id}/channels — canales vinculados del proyecto. */
export async function listChannels(projectId: string): Promise<Channel[]> {
  const res = await http<{ channels: Channel[] }>(`/projects/${projectId}/channels`);
  return res.channels ?? [];
}

/** POST /projects/{id}/channels — vincula connector+target (editor+). */
export async function linkChannel(projectId: string, connector: string, target: string, events = "*"): Promise<void> {
  await http<unknown>(`/projects/${projectId}/channels`, {
    method: "POST",
    body: JSON.stringify({ connector, target, events }),
  });
}

/** DELETE /projects/{id}/channels?connector=&target= — desvincula. */
export async function unlinkChannel(projectId: string, connector: string, target: string): Promise<void> {
  const qs = new URLSearchParams({ connector, target }).toString();
  await http<unknown>(`/projects/${projectId}/channels?${qs}`, { method: "DELETE" });
}

/** POST /channels/{connector}/link-code — código para vincular tu cuenta al bot. */
export async function issueLinkCode(connector: string): Promise<{ code: string; expires_in: number; instructions: string }> {
  return http<{ code: string; expires_in: number; instructions: string }>(`/channels/${connector}/link-code`, {
    method: "POST",
  });
}

export interface ImportResult {
  imported: number;
  skipped: number;
  story_ids: string[];
}

/** POST /projects/{id}/import/github — importa issues de GitHub al backlog (editor+). */
export async function importGitHub(projectId: string, repo?: string): Promise<ImportResult> {
  return http<ImportResult>(`/projects/${projectId}/import/github`, {
    method: "POST",
    body: JSON.stringify(repo ? { repo } : {}),
  });
}

export interface ExportResult {
  repo: string;
  issues_created: number;
  issues_skipped: number;
  labels_created: number;
  deps_created: number;
  deps_skipped: number;
}

/**
 * POST /projects/{id}/export/github — exporta el backlog nativo a GitHub Issues con
 * dependencias `blocked_by` (editor+). SÍNCRONO y lento: crea ~44 issues + ~73 deps,
 * puede tardar 1-3 min. El endpoint es idempotente (un 502 a mitad se reintenta y
 * continúa donde quedó), así que usamos un timeout largo (5 min) en vez del corto por
 * defecto. `repo` opcional: ausente → usa el repo del proyecto.
 */
export async function exportBacklogToGitHub(projectId: string, repo?: string): Promise<ExportResult> {
  if (await isMock()) {
    return { repo: repo ?? "", issues_created: 0, issues_skipped: 0, labels_created: 0, deps_created: 0, deps_skipped: 0 };
  }
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 5 * 60_000);
  const res = await rawFetch(`/projects/${projectId}/export/github`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(repo ? { repo } : {}),
    signal: ctrl.signal,
  }).finally(() => clearTimeout(timer));
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, body || `POST /projects/${projectId}/export/github → ${res.status}`);
  }
  return (await res.json()) as ExportResult;
}

/** POST /projects/{id}/channels/test — envía un mensaje de prueba por el conector real. */
export async function testChannels(projectId: string): Promise<{ sent: number; failed: number; error: string }> {
  return http<{ sent: number; failed: number; error: string }>(`/projects/${projectId}/channels/test`, {
    method: "POST",
  });
}

export type { Channel };

// ─────────────────────────────────────────────────────────────────────────────
// Vista Agentes — cola de PRs + aprobación de workflows (pivote GitHub-native)
// GET /projects/{id}/prs → los PRs abiertos con sus stories ligadas y los
// workflow runs esperando aprobación. Las sesiones activas NO tienen endpoint
// nuevo: son las stories con session_url en running/in_review (usa useTickets).
// ─────────────────────────────────────────────────────────────────────────────

export async function getProjectPRs(projectId: string): Promise<ProjectPR[]> {
  if (await isMock()) return [];
  const res = await http<{ prs?: ProjectPR[] } | ProjectPR[]>(`/projects/${projectId}/prs`);
  return Array.isArray(res) ? res : res?.prs ?? [];
}

// POST /projects/{id}/workflows/{runId}/approve — aprueba un workflow run detenido
// en action_required. 200 → {approved:true, safe:true}. 409 → la política lo
// bloqueó (el PR toca .github/workflows/**): el body trae {approved:false,
// safe:false, reason}; NO es un error de transporte, lo devolvemos como resultado
// para que la UI muestre el motivo en vez de tirar un throw genérico.
export async function approveWorkflowRun(
  projectId: string,
  runId: string,
): Promise<ApproveWorkflowResult> {
  if (await isMock()) return { approved: true, safe: true };
  const res = await rawFetch(
    `/projects/${projectId}/workflows/${encodeURIComponent(runId)}/approve`,
    { method: "POST", headers: { "Content-Type": "application/json" } },
  );
  if (res.status === 409) {
    const body = (await res.json().catch(() => ({}))) as Partial<ApproveWorkflowResult>;
    return { approved: false, safe: false, reason: body.reason };
  }
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(
      res.status,
      body || `POST /projects/${projectId}/workflows/${runId}/approve → ${res.status}`,
    );
  }
  return (await res.json()) as ApproveWorkflowResult;
}

/** Templates github-native: la especialización que el scaffold hornea en cada repo. */
export async function listTemplates(): Promise<string[]> {
  if (await isMock()) return ["_common/AGENTS.md.tmpl", "python-fastapi-react/.github/agents/python-dev.agent.md.tmpl"];
  const r = await http<{ files: string[] }>(`/registry/templates`);
  return r.files;
}

export async function getTemplate(path: string): Promise<string> {
  if (await isMock()) return "# mock template";
  const r = await http<{ content: string }>(`/registry/templates/file?path=${encodeURIComponent(path)}`);
  return r.content;
}

export async function putTemplate(path: string, content: string): Promise<void> {
  if (await isMock()) return;
  await http(`/registry/templates/file?path=${encodeURIComponent(path)}`, {
    method: "PUT",
    body: JSON.stringify({ content }),
  });
}

/** Aplica (re-aplica) la especialización github-native al repo del proyecto. */
export async function scaffoldProject(
  projectId: string,
  stack: string,
): Promise<{ written: string[]; skipped: string[]; missing_vars?: string[] }> {
  if (await isMock()) return { written: [], skipped: [] };
  return http(`/projects/${projectId}/scaffold/github`, {
    method: "POST",
    body: JSON.stringify({ stack }),
  });
}

/** Siembra el secret CLAUDE_CODE_OAUTH_TOKEN en el repo del proyecto (no se guarda en Forja). */
export async function setClaudeSecret(projectId: string, token: string): Promise<void> {
  if (await isMock()) return;
  await http(`/projects/${projectId}/secrets/claude`, {
    method: "PUT",
    body: JSON.stringify({ token }),
  });
}
