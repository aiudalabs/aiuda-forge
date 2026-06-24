"use client";

// Tarjeta de run en el board. RUNNING → live-log embebido (stream del bus). AWAITING → banner de
// human_gate con Rechazar (con motivo) / Revisar diff y aprobar. Click → abre el drawer de detalle.

import type { Run } from "@/lib/types";
import { StatusPill } from "./StatusPill";
import { LiveLog } from "./LiveLog";
import { useApprove, useReject, useLiveEvents } from "@/lib/hooks";

export function RunCard({ run, onOpen }: { run: Run; onOpen: (id: string) => void }) {
  const approve = useApprove();
  const reject = useReject();
  const isRunning = run.status === "RUNNING";
  const isAwaiting = run.status === "AWAITING";
  const events = useLiveEvents(isRunning ? run.id : null, isRunning);

  function doApprove(e: React.MouseEvent) {
    e.stopPropagation();
    approve.mutate([run.id, run.awaitingStep || "human_gate"]);
  }

  function doReject(e: React.MouseEvent) {
    e.stopPropagation();
    const reason = window.prompt(
      "Motivo del rechazo (vuelve al agente como feedback, ronda de fix):",
      "El review encontró un bug de unicode — cubrir ø/ß/中文.",
    );
    if (reason == null) return;
    reject.mutate([run.id, run.awaitingStep || "human_gate", reason]);
  }

  return (
    <div className="run" onClick={() => onOpen(run.id)}>
      <div>
        <div className="tk">
          {run.ticket.id} · {run.id}
          {run.dependsOn?.length ? (
            <span style={{ color: "var(--accent)" }}> · depende de {run.dependsOn.join(", ")}</span>
          ) : null}
        </div>
        <div className="ti">{run.ticket.title}</div>
        <div className="meta">
          {run.agent && (
            <span className="tag">
              {run.agent}
              {run.model ? ` · ${run.model}` : ""}
            </span>
          )}
          {run.currentStep && isRunning && <span className="mono">paso: {run.currentStep}</span>}
          {run.sandbox && <span>🔒 sandbox {run.sandbox}</span>}
          {run.badges.map((b, i) => (
            <span key={i}>{b.label}</span>
          ))}
          <span className="mono">${run.cost.toFixed(2)}</span>
        </div>
      </div>

      <div className="right">
        <StatusPill status={run.status} />
        {run.pr && (
          <a className="prlink" onClick={(e) => e.stopPropagation()}>
            ↗ PR #{run.pr.number}
          </a>
        )}
      </div>

      {isAwaiting && (
        <div className="await-banner">
          <span className="t">
            <b>Human gate</b> — pausada esperando tu visto bueno para abrir el PR.
          </span>
          <button className="btn ghost sm" onClick={doReject} disabled={reject.isPending}>
            Rechazar…
          </button>
          <button className="btn primary sm" onClick={doApprove} disabled={approve.isPending}>
            {approve.isPending ? "Aprobando…" : "Revisar diff y aprobar"}
          </button>
        </div>
      )}

      {isRunning && <LiveLog events={events} />}
    </div>
  );
}
