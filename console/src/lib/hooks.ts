// Hooks de estado de servidor (TanStack Query) + tiempo real (WS). La UI consume estos hooks;
// no toca fetch directamente. Las mutaciones invalidan las queries para reflejar el nuevo estado.

"use client";

import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useState } from "react";
import * as api from "./api";
import type { ApiMode, CreateDesignRunInput, CreateIterationRunInput, CreateStoryInput } from "./api";
import { subscribe } from "./ws";
import type { ProjectSettings, RegistryKind, RunEvent } from "./types";

export const qk = {
  mode: ["mode"] as const,
  // Las queries scopeadas por proyecto (Wave 2) llevan el id en la key para que
  // cambiar de proyecto refetchee en vez de servir caché del proyecto anterior.
  runs: (project?: string | null) => ["runs", project ?? null] as const,
  run: (id: string) => ["run", id] as const,
  events: (id: string) => ["events", id] as const,
  stats: (project?: string | null) => ["stats", project ?? null] as const,
  spendToday: (project?: string | null) => ["spend-today", project ?? null] as const,
  control: ["control"] as const,
  notifications: (project?: string | null) => ["notifications", project ?? null] as const,
  registryList: (kind: RegistryKind) => ["registry", kind] as const,
  registryItem: (kind: RegistryKind, id: string) => ["registry", kind, id] as const,
  settings: ["settings"] as const,
  projectSettings: (id: string | null) => ["projectSettings", id] as const,
  metrics: (project?: string | null) => ["metrics", project ?? null] as const,
  tickets: (project?: string | null) => ["tickets", project ?? null] as const,
  readySprints: (project?: string | null) => ["readySprints", project ?? null] as const,
  epics: ["epics"] as const,
  projects: ["projects"] as const,
};

export function useApiMode() {
  return useQuery<ApiMode>({
    queryKey: qk.mode,
    queryFn: () => api.getApiMode(),
    staleTime: Infinity,
  });
}

export function useRuns(project?: string | null) {
  return useQuery({
    queryKey: qk.runs(project),
    queryFn: () => api.listRuns({ project: project ?? undefined }),
    refetchInterval: 8000,
  });
}

export function useRun(id: string | null) {
  return useQuery({
    queryKey: id ? qk.run(id) : ["run", "none"],
    queryFn: () => api.getRun(id as string),
    enabled: !!id,
  });
}

export function useStats(project?: string | null) {
  return useQuery({
    queryKey: qk.stats(project),
    queryFn: () => api.getStats(project ?? undefined),
    refetchInterval: 10000,
  });
}

export function useSpendToday(project?: string | null) {
  return useQuery({
    queryKey: qk.spendToday(project),
    queryFn: () => api.getSpendToday(project ?? undefined),
    refetchInterval: 15000,
  });
}

export function useControlStatus() {
  return useQuery({ queryKey: qk.control, queryFn: () => api.getControlStatus() });
}

export function useNotifications(project?: string | null) {
  return useQuery({
    queryKey: qk.notifications(project),
    queryFn: () => api.getNotifications(project ?? undefined),
    refetchInterval: 10000,
  });
}

// ── Mutaciones ────────────────────────────────────────────────────────────────

function useRunAction<Args extends unknown[]>(fn: (...args: Args) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: Args) => fn(...args),
    onSuccess: () => {
      // Prefijo (sin el id de proyecto) → invalida todas las variantes scopeadas.
      qc.invalidateQueries({ queryKey: ["runs"] });
      qc.invalidateQueries({ queryKey: ["stats"] });
      qc.invalidateQueries({ queryKey: ["notifications"] });
      // Studio lista los design/iterate runs por separado (["designRuns"]); un
      // delete/cancel/retry de run debe refrescar esa lista también.
      qc.invalidateQueries({ queryKey: ["designRuns"] });
    },
    onError: (err: unknown) => {
      // Surfaceamos el error en consola; el objeto de error queda en mutation.error
      // para que los call sites (RunCard, RunDrawer) puedan renderizarlo si lo desean.
      console.error("[useRunAction] acción falló:", err);
    },
  });
}

export function useApprove() {
  return useRunAction((id: string, step: string) => api.approveStep(id, step));
}
export function useReject() {
  return useRunAction((id: string, step: string, reason: string) => api.rejectStep(id, step, reason));
}
export function useRerunStep() {
  return useRunAction((id: string, step: string) => api.rerunStep(id, step));
}
export function useCancel() {
  return useRunAction((id: string) => api.cancelRun(id));
}
export function useRetry() {
  return useRunAction((id: string) => api.retryRun(id));
}
export function useRequeue() {
  return useRunAction((id: string) => api.requeueRun(id));
}
export function useDeleteRun() {
  return useRunAction((id: string) => api.deleteRun(id));
}
export function useCreateRun() {
  return useRunAction((workflow: string, payload: Record<string, unknown>) =>
    api.createRun({ workflow, payload }),
  );
}

export function usePause() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.pauseFactory(),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.control }),
  });
}
export function useResume() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.resumeFactory(),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.control }),
  });
}

// ── Tiempo real ──────────────────────────────────────────────────────────────
// Suscribe al bus WS (modo real) e invalida las queries afectadas por cada evento.
// En modo mock no hay WS — el live-log se simula desde el detalle (ver useLiveEvents).

export function useRealtime() {
  const qc = useQueryClient();
  const { data: mode } = useApiMode();
  useEffect(() => {
    if (mode !== "real") return;
    const off = subscribe((ev: RunEvent) => {
      qc.invalidateQueries({ queryKey: qk.events(ev.runId) });
      qc.invalidateQueries({ queryKey: qk.run(ev.runId) });
      if (
        ev.type === "run.status_changed" ||
        ev.type === "run.created" ||
        ev.type === "run.done" ||
        ev.type === "run.failed" ||
        ev.type === "run.cancelled" ||
        ev.type === "run.awaiting_approval"
      ) {
        qc.invalidateQueries({ queryKey: ["runs"] });
        qc.invalidateQueries({ queryKey: ["stats"] });
        qc.invalidateQueries({ queryKey: ["notifications"] });
      }
      // El estado de las stories del board cambia cuando un step avanza o el run
      // termina — refrescamos los tickets (todas las variantes scopeadas por
      // proyecto vía el prefijo) para que el Kanban refleje el nuevo estado sin
      // esperar al polling.
      if (
        ev.type === "step.status_changed" ||
        ev.type === "run.done" ||
        ev.type === "run.failed"
      ) {
        qc.invalidateQueries({ queryKey: ["tickets"] });
      }
    });
    return off;
  }, [mode, qc]);
}

// ── Registry hooks ────────────────────────────────────────────────────────────

export function useRegistryList(kind: RegistryKind) {
  return useQuery({
    queryKey: qk.registryList(kind),
    queryFn: () => api.listRegistry(kind),
  });
}

export function useRegistryItem(kind: RegistryKind, id: string | null) {
  return useQuery({
    queryKey: id ? qk.registryItem(kind, id) : ["registry", kind, "none"],
    queryFn: () => api.getRegistryItem(kind, id as string),
    enabled: !!id,
  });
}

export function useAgentPersona(id: string | null) {
  return useQuery({
    queryKey: id ? ["agentPersona", id] : ["agentPersona", "none"],
    queryFn: () => api.getAgentPersona(id as string),
    enabled: !!id,
    staleTime: 60_000,
  });
}

export function useSaveRegistryItem(kind: RegistryKind) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: string }) =>
      api.saveRegistryItem(kind, id, body),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: qk.registryList(kind) });
      qc.invalidateQueries({ queryKey: qk.registryItem(kind, id) });
    },
  });
}

export function useDeleteRegistryItem(kind: RegistryKind) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteRegistryItem(kind, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.registryList(kind) });
    },
  });
}

// ── Settings hooks ────────────────────────────────────────────────────────────

export function useSettings() {
  return useQuery({ queryKey: qk.settings, queryFn: () => api.getSettings() });
}

export function useSaveSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.saveSettings,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.settings });
    },
  });
}

// ── Per-project settings hooks (Wave 2) ───────────────────────────────────────
// execution_unit + merge_mode del proyecto activo. GET/PUT /projects/{id}/settings.

export function useProjectSettings(projectId: string | null) {
  return useQuery({
    queryKey: qk.projectSettings(projectId),
    queryFn: () => api.getProjectSettings(projectId as string),
    enabled: !!projectId,
  });
}

export function useSaveProjectSettings(projectId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: ProjectSettings) =>
      api.saveProjectSettings(projectId as string, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.projectSettings(projectId) });
    },
  });
}

// ── Dispatch hooks (pivote F2) ────────────────────────────────────────────────
// El ready-set del conductor + el despacho en modo approve.

export function useDispatchCandidates(projectId: string | null) {
  return useQuery({
    queryKey: ["dispatchCandidates", projectId] as const,
    queryFn: () => api.getDispatchCandidates(projectId as string),
    enabled: !!projectId,
    refetchInterval: 30000,
  });
}

export function useDispatch(projectId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (unit: { story_id?: string; sprint_id?: string; executor?: string }) =>
      api.dispatchWork(projectId as string, unit),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dispatchCandidates", projectId] });
      qc.invalidateQueries({ queryKey: ["tickets"] });
    },
  });
}

export function useRequeueStory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (storyId: string) => api.requeueStory(storyId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tickets"] });
      qc.invalidateQueries({ queryKey: ["dispatchCandidates"] });
    },
  });
}

// Reencola TODAS las stories `failed` de un sprint de una (R2 en bloque). Devuelve
// {requeued, skipped} para que la vista muestre cuántas volvieron y cuáles se
// saltaron con su razón. Invalida tickets/candidatos como el requeue individual.
export function useRequeueSprint() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (sprintId: string) => api.requeueSprint(sprintId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tickets"] });
      qc.invalidateQueries({ queryKey: ["dispatchCandidates"] });
    },
  });
}

// Borra UNA story (local, no toca GitHub). Invalida los tickets del proyecto para
// que desaparezca de tabla/kanban/DAG. El 409 (dependientes) y el 404 (cross-tenant)
// llegan como ApiError para que el call site los muestre.
export function useDeleteStory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, project }: { id: string; project?: string }) => api.deleteStory(id, project),
    onSuccess: (_data, { project }) => {
      qc.invalidateQueries({ queryKey: qk.tickets(project ?? null) });
      qc.invalidateQueries({ queryKey: qk.tickets(null) });
      qc.invalidateQueries({ queryKey: ["dispatchCandidates"] });
    },
  });
}

// "Enviar a GitHub" por story: export síncrono. Invalida los tickets al terminar
// (el backend anota external_ref). El error real (sin repo / GitHub) viaja como ApiError.
export function useExportStory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, project }: { id: string; project?: string }) => api.exportStory(id, project),
    onSuccess: (_data, { project }) => {
      qc.invalidateQueries({ queryKey: qk.tickets(project ?? null) });
      qc.invalidateQueries({ queryKey: qk.tickets(null) });
      qc.invalidateQueries({ queryKey: ["dispatchCandidates"] });
    },
  });
}

// ── Metrics hook ──────────────────────────────────────────────────────────────

export function useMetrics(project?: string | null) {
  return useQuery({
    queryKey: qk.metrics(project),
    queryFn: () => api.getMetrics(project ?? undefined),
    refetchInterval: 15000,
  });
}

// ── GitHub spend hook (F4) ──────────────────────────────────────────────────
// Facturación de GitHub del ciclo actual; refetch cada 5 min (cambia despacio).

export function useGitHubSpend(projectId?: string | null) {
  return useQuery({
    queryKey: ["githubSpend", projectId] as const,
    queryFn: () => api.getGitHubSpend(projectId as string),
    enabled: !!projectId,
    refetchInterval: 300000,
  });
}

// ── Tickets hook (orquestador) ────────────────────────────────────────────────

export function useTickets(project?: string | null) {
  return useQuery({
    queryKey: qk.tickets(project),
    queryFn: () => api.listTickets(project ?? undefined),
    refetchInterval: 10000,
  });
}

export function useReadySprints(project?: string | null) {
  return useQuery({
    queryKey: qk.readySprints(project),
    queryFn: () => api.readySprints(project ?? undefined),
    refetchInterval: 10000,
  });
}

export function useEpics() {
  return useQuery({
    queryKey: qk.epics,
    queryFn: () => api.listEpics(),
    staleTime: 60_000,
  });
}

export function useCreateStory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateStoryInput) => api.createStory(input),
    onSuccess: (_data, input) => {
      // Invalida los tickets del proyecto de la story (y la variante sin scope)
      // para que aparezca en tabla, kanban y DAG del proyecto correcto.
      qc.invalidateQueries({ queryKey: qk.tickets(input.project_id) });
      qc.invalidateQueries({ queryKey: qk.tickets(null) });
    },
  });
}

// Exporta el backlog nativo del proyecto a GitHub Issues con deps. La mutación
// invalida los tickets al terminar (el backend puede anotar external_ref/issue #).
export function useExportBacklog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ projectId, repo }: { projectId: string; repo?: string }) =>
      api.exportBacklogToGitHub(projectId, repo),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tickets"] });
    },
  });
}

// ── Projects hooks ────────────────────────────────────────────────────────────

export function useProjects() {
  return useQuery({
    queryKey: qk.projects,
    queryFn: () => api.listProjects(),
    staleTime: 30_000,
  });
}

// Sprints (planned increments with name + goal) for the Sprints backlog view.
export function useSprints() {
  return useQuery({
    queryKey: ["sprints"],
    queryFn: () => api.listSprints(),
    staleTime: 60_000,
  });
}

// Studio = Confluence (U1): the project's repo docs/ tree + a single doc's content.
export function useProjectDocs(projectId: string | null) {
  return useQuery({
    queryKey: ["project-docs", projectId],
    queryFn: () => api.listProjectDocs(projectId as string),
    enabled: !!projectId,
    staleTime: 60_000,
  });
}

export function useProjectDoc(projectId: string | null, path: string | null, ref = "design") {
  return useQuery({
    queryKey: ["project-doc", projectId, path, ref],
    queryFn: () => api.getProjectDoc(projectId as string, path as string, ref),
    enabled: !!projectId && !!path,
    staleTime: 60_000,
  });
}

// Version history (commits on the design branch) of one doc — for the version chips.
export function useDocHistory(projectId: string | null, path: string | null) {
  return useQuery({
    queryKey: ["doc-history", projectId, path],
    queryFn: () => api.getDocHistory(projectId as string, path as string),
    enabled: !!projectId && !!path,
    staleTime: 30_000,
  });
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, description }: { name: string; description: string }) =>
      api.createProject(name, description),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.projects });
    },
  });
}

// ── Studio / Design runs hooks ────────────────────────────────────────────────

export function useDesignRuns() {
  return useQuery({
    queryKey: ["designRuns"],
    queryFn: () => api.listDesignRuns(),
    refetchInterval: 8000,
  });
}

export function useDesignRun(id: string | null) {
  return useQuery({
    queryKey: id ? ["designRun", id] : ["designRun", "none"],
    queryFn: () => api.getDesignRun(id as string),
    enabled: !!id,
    refetchInterval: 3000, // live: las fases avanzan solas
  });
}

// Variante de sondeo lento para las CARDS de proyecto: GET /runs no incluye
// steps, así que el progreso de fases solo existe en el detalle del run.
// Misma queryKey que useDesignRun → comparte cache con el panel de detalle
// (el run seleccionado se sigue refrescando a 3s por su propio observer).
export function useDesignRunSummary(id: string) {
  return useQuery({
    queryKey: ["designRun", id],
    queryFn: () => api.getDesignRun(id),
    refetchInterval: 15000,
  });
}

export function useCreateDesignRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateDesignRunInput) => api.createDesignRun(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["designRuns"] });
    },
  });
}

export function useCreateIterationRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateIterationRunInput) => api.createIterationRun(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["designRuns"] });
    },
  });
}

/** Artefacto (markdown) producido por un paso del design run. */
export function useArtifact(runId: string | null, stepId: string | null) {
  return useQuery({
    queryKey: ["artifact", runId, stepId],
    queryFn: () => api.getArtifact(runId as string, stepId as string),
    enabled: !!runId && !!stepId,
    staleTime: 30_000, // los artefactos no cambian frecuentemente
  });
}

// ── Vista Agentes — cola de PRs + aprobación de workflows ─────────────────────
// Los PRs abiertos del proyecto con stories ligadas + workflow runs pendientes.
// Polling suave (20s): la actividad de PRs es de minutos, no de segundos.

export function useProjectPRs(projectId: string | null) {
  return useQuery({
    queryKey: ["projectPRs", projectId] as const,
    queryFn: () => api.getProjectPRs(projectId as string),
    enabled: !!projectId,
    refetchInterval: 20000,
  });
}

export function useApproveWorkflow(projectId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (runId: string) => api.approveWorkflowRun(projectId as string, runId),
    onSuccess: (res) => {
      // Solo refrescamos si la aprobación pasó — un 409 (política) deja el run en
      // su sitio, no hay nada nuevo que traer.
      if (res.approved) qc.invalidateQueries({ queryKey: ["projectPRs", projectId] });
    },
  });
}

// Despacha una resolución de conflicto del PR contra main (incidente #83). Al
// despachar, el agente pasa a running en GitHub y su PR se actualizará: refrescamos
// la cola para reflejar el cambio de estado.
export function useResolveConflicts(projectId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (prNumber: number) => api.resolvePRConflicts(projectId as string, prNumber),
    onSuccess: (res) => {
      if (res.dispatched) qc.invalidateQueries({ queryKey: ["projectPRs", projectId] });
    },
  });
}

/**
 * Stream de eventos de un run para el live-log. En modo real: replay (GET events) + push WS.
 * En modo mock: replay del mock y, si el run está corriendo, un "tick" simulado que va
 * revelando los eventos para que se vea vivo.
 */
export function useLiveEvents(runId: string | null, isRunning: boolean) {
  const { data: mode } = useApiMode();
  const [events, setEvents] = useState<RunEvent[]>([]);

  useEffect(() => {
    if (!runId) {
      setEvents([]);
      return;
    }
    let cancelled = false;
    let lastId = 0;

    async function load() {
      const initial = await api.getEvents(runId as string, 0);
      if (cancelled) return;
      if (mode === "mock" && isRunning && initial.length > 1) {
        // Simula el stream: revela uno a uno.
        setEvents(initial.slice(0, 1));
        let i = 1;
        const timer = setInterval(() => {
          if (cancelled || i >= initial.length) {
            clearInterval(timer);
            return;
          }
          setEvents(initial.slice(0, i + 1));
          i++;
        }, 1400);
        return () => clearInterval(timer);
      }
      setEvents(initial);
      lastId = initial.length ? initial[initial.length - 1].id : 0;
    }

    const cleanup = load();

    let off: (() => void) | undefined;
    if (mode === "real") {
      off = subscribe((ev) => {
        if (ev.runId === runId && ev.id > lastId) {
          lastId = ev.id;
          setEvents((prev) => [...prev, ev]);
        }
      });
    }

    return () => {
      cancelled = true;
      off?.();
      void cleanup;
    };
  }, [runId, mode, isRunning]);

  return events;
}

/** Estado de la conexión GitHub del usuario + config de la App (Settings → Conexiones). */
export function useGitHubStatus() {
  return useQuery({
    queryKey: ["githubStatus"],
    queryFn: () => api.getGitHubStatus(),
    staleTime: 30_000,
  });
}

/** Canales disponibles en el GitHub del proyecto (probe real; cache 60s). */
export function useExecutors(projectId: string | null) {
  return useQuery({
    queryKey: ["executors", projectId],
    queryFn: () => api.getExecutors(projectId as string),
    enabled: !!projectId,
    staleTime: 60_000,
  });
}

/** Archivos de template github-native (especialización repo-baked). */
export function useTemplates() {
  return useQuery({ queryKey: ["templates"], queryFn: api.listTemplates, staleTime: 60_000 });
}

export function useTemplate(path: string | null) {
  return useQuery({
    queryKey: ["template", path],
    queryFn: () => api.getTemplate(path as string),
    enabled: !!path,
  });
}

export function useSaveTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, content }: { path: string; content: string }) => api.putTemplate(path, content),
    onSuccess: (_d, v) => qc.invalidateQueries({ queryKey: ["template", v.path] }),
  });
}

export function useScaffoldProject(projectId: string | null) {
  return useMutation({
    mutationFn: (stack: string) => api.scaffoldProject(projectId as string, stack),
  });
}

export function useSetClaudeSecret(projectId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (token: string) => api.setClaudeSecret(projectId as string, token),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["executors", projectId] }),
  });
}
