"use client";

// Drawer de detalle del run (doc 16 §2.2 + §10): timeline de pasos, stream de eventos, DIFF
// propuesto (revisar antes de aprobar), costo por paso, y acciones aprobar/rechazar-con-motivo,
// cancelar, reintentar, borrar. Todas = endpoints del contrato.

import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { RunStep, StepStatus } from "@/lib/types";
import { stepSignal, isLongDetail } from "@/lib/stepSignals";
import { StatusPill } from "./StatusPill";
import { LiveLog } from "./LiveLog";
import { DiffBox } from "./DiffBox";
import {
  useApprove,
  useCancel,
  useDeleteRun,
  useLiveEvents,
  useReject,
  useRetry,
  useRun,
} from "@/lib/hooks";

const STEP_UI: Record<StepStatus, { cls: string; icon: string }> = {
  DONE: { cls: "ok", icon: "✓" },
  AWAITING: { cls: "aw", icon: "⏸" },
  RUNNING: { cls: "run", icon: "›" },
  FAILED: { cls: "aw", icon: "✕" },
  QUEUED: { cls: "", icon: "·" },
  SKIPPED: { cls: "", icon: "–" },
};

function StepRow({ step }: { step: RunStep }) {
  const ui = STEP_UI[step.status] ?? STEP_UI.QUEUED;
  const signal = stepSignal(step);
  const collapsible = isLongDetail(step.detail);
  const [open, setOpen] = useState(false);

  return (
    <div className={`step ${ui.cls}`}>
      <div className="si">{ui.icon}</div>
      <div style={{ minWidth: 0, flex: 1 }}>
        <div className="sn">
          {step.id}{" "}
          {(step.agent || step.model) && (
            <span style={{ fontWeight: 400, color: "var(--ink4)" }}>
              · {[step.agent, step.model].filter(Boolean).join(" (")}
              {step.model ? ")" : ""}
            </span>
          )}
          {typeof step.cost === "number" && (
            <span style={{ fontWeight: 400, color: "var(--ink4)" }}> · ${step.cost.toFixed(2)}</span>
          )}
        </div>

        {/* Titular escaneable: la señal derivada (tests / verdict / PR), no el blob. */}
        {signal && (
          <div className="step-summary">
            <span className={`step-sig ${signal.tone}`}>{signal.summary}</span>
            {signal.pr && (
              <a
                className="step-prlink"
                href={signal.pr.url || "#"}
                onClick={(e) => !signal.pr?.url && e.preventDefault()}
                target={signal.pr.url ? "_blank" : undefined}
                rel="noreferrer"
              >
                ↗ PR #{signal.pr.number}
              </a>
            )}
            {collapsible && (
              <button
                className="step-toggle"
                onClick={() => setOpen((v) => !v)}
                aria-expanded={open}
              >
                {open ? "ocultar detalle" : "ver detalle"}
              </button>
            )}
          </div>
        )}

        {/* Detalle completo: markdown, colapsado por defecto si es largo. */}
        {step.detail && (!collapsible || open) && (
          <div className="step-detail artifact-md">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{step.detail}</ReactMarkdown>
          </div>
        )}
      </div>
    </div>
  );
}

export function RunDrawer({ runId, onClose }: { runId: string | null; onClose: () => void }) {
  const { data: run, isLoading, isError } = useRun(runId);
  const events = useLiveEvents(runId, run?.status === "RUNNING");
  const approve = useApprove();
  const reject = useReject();
  const cancel = useCancel();
  const retry = useRetry();
  const del = useDeleteRun();

  const open = !!runId;
  const awaitingStep = run?.awaitingStep || run?.steps?.find((s) => s.status === "AWAITING")?.id || "human_gate";

  function doApprove() {
    if (!run) return;
    approve.mutate([run.id, awaitingStep], { onSuccess: onClose });
  }
  function doReject() {
    if (!run) return;
    const reason = window.prompt(
      "Motivo del rechazo / cambios pedidos (vuelve al agente como feedback):",
      "El review encontró un bug de unicode — cubrir ø/ß/中文.",
    );
    if (reason == null) return;
    reject.mutate([run.id, awaitingStep, reason], { onSuccess: onClose });
  }
  function doCancel() {
    if (!run || !window.confirm("¿Cancelar este run?")) return;
    cancel.mutate([run.id], { onSuccess: onClose });
  }
  function doDelete() {
    if (!run || !window.confirm("¿Borrar este run? Esta acción es destructiva.")) return;
    del.mutate([run.id], { onSuccess: onClose });
  }

  const isAwaiting = run?.status === "AWAITING";
  const isFailed = run?.status === "FAILED" || run?.status === "CANCELLED";

  return (
    <>
      <div className={`overlay ${open ? "on" : ""}`} onClick={onClose} />
      <aside className={`drawer ${open ? "on" : ""}`} aria-hidden={!open}>
        {run && (
          <>
            <div className="dh">
              <div>
                <h3>{run.ticket.title}</h3>
                <div className="tk">
                  {run.ticket.id} · {run.id} · {run.workflow}
                </div>
              </div>
              <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <StatusPill status={run.status} />
                <button className="x" onClick={onClose}>
                  ✕
                </button>
              </div>
            </div>

            <div className="db">
              <div className="eyebrow acc">Pasos</div>
              <div className="steps">
                {run.steps.map((s, i) => (
                  // step ids repeat across on_fail loops (implement/gate/review run
                  // multiple times), so the id alone is NOT a unique key — include the index.
                  <StepRow key={`${s.id}-${i}`} step={s} />
                ))}
              </div>

              <div className="eyebrow acc" style={{ marginTop: 18 }}>
                Stream de eventos
              </div>
              <LiveLog events={events} style={{ marginTop: 8, maxHeight: "none" }} />

              {run.diff && (
                <>
                  <div className="eyebrow acc" style={{ marginTop: 18 }}>
                    Diff propuesto · revísalo antes de aprobar
                  </div>
                  <DiffBox diff={run.diff} />
                </>
              )}

              {run.costBreakdown && (
                <>
                  <div className="eyebrow" style={{ marginTop: 16 }}>
                    Costo por paso
                  </div>
                  <div
                    style={{
                      fontFamily: "var(--mono)",
                      fontSize: 12,
                      color: "var(--ink3)",
                      marginTop: 6,
                    }}
                  >
                    {run.costBreakdown.byStep?.map((b, i) => (
                      <span key={`${b.step}-${i}`}>
                        {i > 0 ? " · " : ""}
                        {b.step} ${b.cost.toFixed(2)}
                      </span>
                    ))}
                    {run.costBreakdown.byStep?.length ? " · " : ""}
                    <b style={{ color: "var(--ink)" }}>total ${run.costBreakdown.total.toFixed(2)}</b>
                  </div>
                </>
              )}

              {/* Acciones */}
              {isAwaiting && (
                <div style={{ display: "flex", gap: 10, marginTop: 18 }}>
                  <button className="btn ghost" style={{ flex: 1 }} onClick={doReject} disabled={reject.isPending}>
                    Rechazar / pedir cambios
                  </button>
                  <button className="btn primary" style={{ flex: 1 }} onClick={doApprove} disabled={approve.isPending}>
                    {approve.isPending ? "Aprobando…" : "Aprobar y abrir PR"}
                  </button>
                </div>
              )}

              <div style={{ display: "flex", gap: 10, marginTop: 12 }}>
                {isFailed ? (
                  <button className="btn ghost sm" onClick={() => retry.mutate([run.id])} disabled={retry.isPending}>
                    ↻ Reintentar
                  </button>
                ) : (
                  !isAwaiting &&
                  run.status !== "DONE" && (
                    <button className="btn ghost sm" onClick={doCancel} disabled={cancel.isPending}>
                      Cancelar
                    </button>
                  )
                )}
                <button className="btn ghost sm" onClick={doDelete} disabled={del.isPending}>
                  Borrar
                </button>
                {run.pr && (
                  <a className="btn ghost sm" href={run.pr.url || "#"} onClick={(e) => !run.pr?.url && e.preventDefault()}>
                    ↗ Ver PR #{run.pr.number}
                  </a>
                )}
              </div>
            </div>
          </>
        )}

        {open && isLoading && (
          <div className="db">
            <span className="spin" /> cargando run…
          </div>
        )}
        {open && isError && (
          <div className="db">
            <div className="placeholder err">No se pudo cargar el run.</div>
          </div>
        )}
      </aside>
    </>
  );
}
