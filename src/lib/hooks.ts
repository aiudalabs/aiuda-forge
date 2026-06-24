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
import type { ApiMode } from "./api";
import { subscribe } from "./ws";
import type { RunEvent } from "./types";

export const qk = {
  mode: ["mode"] as const,
  runs: ["runs"] as const,
  run: (id: string) => ["run", id] as const,
  events: (id: string) => ["events", id] as const,
  stats: ["stats"] as const,
  control: ["control"] as const,
  notifications: ["notifications"] as const,
};

export function useApiMode() {
  return useQuery<ApiMode>({
    queryKey: qk.mode,
    queryFn: () => api.getApiMode(),
    staleTime: Infinity,
  });
}

export function useRuns() {
  return useQuery({ queryKey: qk.runs, queryFn: () => api.listRuns(), refetchInterval: 8000 });
}

export function useRun(id: string | null) {
  return useQuery({
    queryKey: id ? qk.run(id) : ["run", "none"],
    queryFn: () => api.getRun(id as string),
    enabled: !!id,
  });
}

export function useStats() {
  return useQuery({ queryKey: qk.stats, queryFn: () => api.getStats(), refetchInterval: 10000 });
}

export function useSpendToday() {
  return useQuery({ queryKey: ["spend-today"], queryFn: () => api.getSpendToday(), refetchInterval: 15000 });
}

export function useControlStatus() {
  return useQuery({ queryKey: qk.control, queryFn: () => api.getControlStatus() });
}

export function useNotifications() {
  return useQuery({ queryKey: qk.notifications, queryFn: () => api.getNotifications(), refetchInterval: 10000 });
}

// ── Mutaciones ────────────────────────────────────────────────────────────────

function useRunAction<Args extends unknown[]>(fn: (...args: Args) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: Args) => fn(...args),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.runs });
      qc.invalidateQueries({ queryKey: qk.stats });
      qc.invalidateQueries({ queryKey: qk.notifications });
    },
  });
}

export function useApprove() {
  return useRunAction((id: string, step: string) => api.approveStep(id, step));
}
export function useReject() {
  return useRunAction((id: string, step: string, reason: string) => api.rejectStep(id, step, reason));
}
export function useCancel() {
  return useRunAction((id: string) => api.cancelRun(id));
}
export function useRetry() {
  return useRunAction((id: string) => api.retryRun(id));
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
        qc.invalidateQueries({ queryKey: qk.runs });
        qc.invalidateQueries({ queryKey: qk.stats });
        qc.invalidateQueries({ queryKey: qk.notifications });
      }
    });
    return off;
  }, [mode, qc]);
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
