"use client";

// BOARD · Runs — la pantalla por defecto, cableada en vivo contra el control-plane.
// Stat cards · pausar/reanudar fábrica · lista de runs (live-log / human-gate) · drawer de
// detalle. Estados: vacío, cargando, error. AWAITING resaltado + notificación en la campana.

import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { StatCards } from "./StatCards";
import { RunCard } from "./RunCard";
import { RunDrawer } from "./RunDrawer";
import { useControlStatus, usePause, useResume, useRuns } from "@/lib/hooks";
import { useActiveProjectId } from "@/lib/activeProject";
import type { RunStatus } from "@/lib/types";

const STATUS_OPTS: { value: RunStatus | "ALL" | "ATTENTION"; label: string }[] = [
  { value: "ALL", label: "Todos" },
  { value: "ATTENTION", label: "Necesita atención" },
  { value: "RUNNING", label: "Running" },
  { value: "AWAITING", label: "Awaiting" },
  { value: "DONE", label: "Done" },
  { value: "FAILED", label: "Failed" },
];

export function BoardView() {
  const params = useSearchParams();
  const projectId = useActiveProjectId();
  const { data: runs, isLoading, isError, refetch } = useRuns(projectId);
  const { data: control } = useControlStatus();
  const pause = usePause();
  const resume = useResume();

  const [openRunId, setOpenRunId] = useState<string | null>(null);
  const [filter, setFilter] = useState<RunStatus | "ALL" | "ATTENTION">("ALL");
  const [q, setQ] = useState("");

  // Deep-link desde la campana: /?run=<id>
  useEffect(() => {
    const r = params.get("run");
    if (r) setOpenRunId(r);
  }, [params]);

  // Cerrar drawer con Escape.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpenRunId(null);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const filtered = useMemo(() => {
    let list = runs ?? [];
    if (filter === "ATTENTION") list = list.filter((r) => r.status === "AWAITING" || r.status === "FAILED");
    else if (filter !== "ALL") list = list.filter((r) => r.status === filter);
    if (q.trim()) {
      const needle = q.toLowerCase();
      list = list.filter(
        (r) =>
          r.ticket.id.toLowerCase().includes(needle) ||
          r.ticket.title.toLowerCase().includes(needle) ||
          r.id.toLowerCase().includes(needle),
      );
    }
    return list;
  }, [runs, filter, q]);

  const paused = control?.paused ?? false;

  return (
    <div className="wrap">
      <StatCards />

      <div className="sectitle">
        <h2>Corridas</h2>
        <span className="c">workflow: factory · {runs?.length ?? 0} runs</span>
        <span className="sp" />
        <select className="inp" style={{ width: "auto" }} value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          {STATUS_OPTS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <input
          className="inp"
          style={{ width: 180 }}
          placeholder="Buscar ticket / run…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        {paused ? (
          <button className="btn primary sm" onClick={() => resume.mutate()} disabled={resume.isPending}>
            ▶ Reanudar fábrica
          </button>
        ) : (
          <button className="btn ghost sm" onClick={() => pause.mutate()} disabled={pause.isPending}>
            ⏸ Pausar fábrica
          </button>
        )}
      </div>

      {paused && (
        <div className="shellnote" style={{ borderColor: "var(--accent-line)" }}>
          <span className="dot paused" /> Fábrica <b style={{ color: "var(--accent)" }}>pausada</b> — no
          se entrega trabajo nuevo; lo en curso sigue.
        </div>
      )}

      {/* Estados */}
      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic">
            <span className="spin" />
          </div>
          Cargando corridas…
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          No se pudo conectar al control-plane.{" "}
          <button className="btn ghost sm" onClick={() => refetch()}>
            Reintentar
          </button>
        </div>
      ) : filtered.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">▦</div>
          {runs && runs.length > 0 ? "Ningún run coincide con el filtro." : "Sin corridas todavía."}
          <div style={{ marginTop: 6, fontSize: 13, color: "var(--ink4)" }}>
            Los runs aparecen cuando el orquestador toma un ticket READY, o al lanzar uno manualmente.
          </div>
        </div>
      ) : (
        <div className="runs">
          {filtered.map((run) => (
            <RunCard key={run.id} run={run} onOpen={setOpenRunId} />
          ))}
        </div>
      )}

      <div className="quote serif">
        El gate verifica que <span className="acc">pasa los tests</span>; el revisor cross-model
        encuentra <span className="acc">lo que los tests no ven</span>. La fábrica no fusiona sola lo
        que importa.
      </div>

      <RunDrawer runId={openRunId} onClose={() => setOpenRunId(null)} />
    </div>
  );
}
