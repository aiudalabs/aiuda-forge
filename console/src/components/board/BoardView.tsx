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
import { useT } from "@/lib/i18n";
import type { RunStatus } from "@/lib/types";

const STATUS_OPTS: { value: RunStatus | "ALL" | "ATTENTION"; key: string }[] = [
  { value: "ALL", key: "board.filter.all" },
  { value: "ATTENTION", key: "board.filter.attention" },
  { value: "RUNNING", key: "board.filter.running" },
  { value: "AWAITING", key: "board.filter.awaiting" },
  { value: "DONE", key: "board.filter.done" },
  { value: "FAILED", key: "board.filter.failed" },
];

export function BoardView() {
  const t = useT();
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
        <h2>{t("board.heading")}</h2>
        <span className="c">{t("board.runsMeta", { count: runs?.length ?? 0 })}</span>
        <span className="sp" />
        <select className="inp" style={{ width: "auto" }} value={filter} onChange={(e) => setFilter(e.target.value as typeof filter)}>
          {STATUS_OPTS.map((o) => (
            <option key={o.value} value={o.value}>
              {t(o.key)}
            </option>
          ))}
        </select>
        <input
          className="inp"
          style={{ width: 180 }}
          placeholder={t("board.searchPlaceholder")}
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        {paused ? (
          <button className="btn primary sm" onClick={() => resume.mutate()} disabled={resume.isPending}>
            {t("board.resume")}
          </button>
        ) : (
          <button className="btn ghost sm" onClick={() => pause.mutate()} disabled={pause.isPending}>
            {t("board.pause")}
          </button>
        )}
      </div>

      {paused && (
        <div className="shellnote" style={{ borderColor: "var(--accent-line)" }}>
          <span className="dot paused" /> {t("board.pausedNote.factory")}{" "}
          <b style={{ color: "var(--accent)" }}>{t("board.pausedNote.paused")}</b>{" "}
          {t("board.pausedNote.rest")}
        </div>
      )}

      {/* Estados */}
      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic">
            <span className="spin" />
          </div>
          {t("board.loading")}
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("board.error")}{" "}
          <button className="btn ghost sm" onClick={() => refetch()}>
            {t("board.retry")}
          </button>
        </div>
      ) : filtered.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">▦</div>
          {runs && runs.length > 0 ? t("board.emptyFiltered") : t("board.empty")}
          <div style={{ marginTop: 6, fontSize: 13, color: "var(--ink4)" }}>
            {t("board.emptyHint")}
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
        {t("board.quote.1")} <span className="acc">{t("board.quote.2")}</span>
        {t("board.quote.3")} <span className="acc">{t("board.quote.4")}</span>
        {t("board.quote.5")}
      </div>

      <RunDrawer runId={openRunId} onClose={() => setOpenRunId(null)} />
    </div>
  );
}
