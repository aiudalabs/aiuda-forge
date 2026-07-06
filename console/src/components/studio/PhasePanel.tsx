"use client";

// Panel de una fase: artefacto (markdown/mockup/backlog) + aprobar/rechazar el gate.
// CRÍTICO: el flujo aprobar/rechazar (useApprove/useReject) se preserva exacto —
// es la acción más importante del Studio, no tocar sin verificar el gate primero.

import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  useArtifact,
  useApprove,
  useRerunStep,
  useLiveEvents,
  useReject,
} from "@/lib/hooks";
import { LiveLog } from "@/components/board/LiveLog";
import { useT } from "@/lib/i18n";
import type { DesignPhase, DesignStepStatus } from "@/lib/types";
import { BacklogArtifact } from "./BacklogArtifact";
import { BACKLOG_STEPS, phaseLabel, type PhaseState } from "./phaseHelpers";

export function PhasePanel({
  runId,
  phase,
  state,
}: {
  runId: string;
  phase: DesignPhase;
  state: PhaseState;
}) {
  const t = useT();
  const hasArtifact = phase.designStatus === "DONE";
  const isRunning = phase.designStatus === "RUNNING";
  // Live-log de la fase en curso: mismo stream WS que el board, pero compacto.
  const liveEvents = useLiveEvents(runId, isRunning);

  const {
    data: artifactText,
    isLoading: artLoading,
    isError: artError,
  } = useArtifact(runId, hasArtifact ? phase.stepId : null);

  const approve = useApprove();
  const reject = useReject();
  const rerun = useRerunStep();
  const [rejectInput, setRejectInput] = useState("");
  const [showRejectForm, setShowRejectForm] = useState(false);

  const gateStepId = phase.gateId || `${phase.stepId}_gate`;

  function doApprove() {
    approve.mutate([runId, gateStepId]);
  }

  function doRerun() {
    rerun.mutate([runId, phase.stepId]);
  }

  function doReject() {
    if (!rejectInput.trim()) return;
    reject.mutate([runId, gateStepId, rejectInput.trim()], {
      onSuccess: () => {
        setShowRejectForm(false);
        setRejectInput("");
      },
    });
  }

  return (
    <div className="phase-panel">
      {/* Cabecera de fase */}
      <div className="phase-panel-header">
        <div>
          <h3 className="phase-panel-title">{phaseLabel(t, phase.stepId, phase.name)}</h3>
          <PhaseStatusBadge state={state} stepId={phase.stepId} designStatus={phase.designStatus} />
        </div>
      </div>

      {/* Artefacto */}
      <div className="artifact-box">
        {!hasArtifact && (
          <div className="artifact-empty">
            {isRunning ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 10, width: "100%", textAlign: "left", alignItems: "stretch" }}>
                <span style={{ alignSelf: "center" }}><span className="spin" style={{ width: 14, height: 14 }} /> {t("studio.view.generatingDoc")}</span>
                <LiveLog events={liveEvents} style={{ marginTop: 0, maxHeight: 220 }} />
              </div>
            ) : (
              <span style={{ color: "var(--ink4)" }}>{t("studio.view.docWillShow")}</span>
            )}
          </div>
        )}

        {hasArtifact && artLoading && (
          <div className="artifact-empty">
            <span className="spin" style={{ width: 14, height: 14 }} /> {t("studio.view.loadingArtifact")}
          </div>
        )}

        {hasArtifact && artError && (
          <div className="artifact-empty" style={{ color: "var(--accent)" }}>
            {t("studio.view.loadDocError")}
          </div>
        )}

        {hasArtifact && artifactText && phase.stepId === "mockups" && (
          <MockupsArtifact html={artifactText} />
        )}

        {hasArtifact && artifactText && BACKLOG_STEPS.has(phase.stepId) && (
          <BacklogArtifact raw={artifactText} />
        )}

        {hasArtifact && artifactText && phase.stepId !== "mockups" && !BACKLOG_STEPS.has(phase.stepId) && (
          <div className="artifact-md">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{artifactText}</ReactMarkdown>
          </div>
        )}
      </div>

      {/* Acciones: solo cuando el gate está AWAITING */}
      {state === "awaiting" && phase.stepId !== "handoff" && (
        <div className="phase-actions">
          {!showRejectForm ? (
            <>
              <button
                className="btn ghost"
                onClick={() => setShowRejectForm(true)}
                disabled={reject.isPending || approve.isPending}
              >
                {t("studio.view.reject")}
              </button>
              <button
                className="btn primary"
                onClick={doApprove}
                disabled={approve.isPending || reject.isPending}
              >
                {approve.isPending ? t("studio.view.approving") : t("studio.view.approve")}
              </button>
            </>
          ) : (
            <div className="reject-form">
              <textarea
                className="inp"
                style={{ resize: "vertical", minHeight: 72, fontSize: 13 }}
                placeholder={t("studio.view.rejectPlaceholder")}
                value={rejectInput}
                onChange={(e) => setRejectInput(e.target.value)}
                autoFocus
              />
              <div style={{ display: "flex", gap: 8 }}>
                <button
                  className="btn ghost sm"
                  onClick={() => {
                    setShowRejectForm(false);
                    setRejectInput("");
                  }}
                >
                  {t("studio.view.cancel")}
                </button>
                <button
                  className="btn primary sm"
                  onClick={doReject}
                  disabled={!rejectInput.trim() || reject.isPending}
                >
                  {reject.isPending ? t("studio.view.sending") : t("studio.view.sendFeedback")}
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Estado final: aprobada o fallida */}
      {state === "approved" && (
        <div className="phase-banner ok">
          {t("studio.view.phaseApproved")}
        </div>
      )}
      {/* Regenerar: rehacer esta fase (vuelve a su gate para re-aprobación). Solo fases de diseño (con gate). */}
      {state === "approved" && phase.gateId && (
        <div className="phase-actions">
          <button className="btn ghost sm" onClick={doRerun} disabled={rerun.isPending}>
            {rerun.isPending ? t("studio.view.regenerating") : t("studio.view.regenerate")}
          </button>
        </div>
      )}
      {state === "failed" && (
        <>
          <div className="phase-banner fail">{t("studio.view.phaseFailed")}</div>
          {phase.gateId && (
            <div className="phase-actions">
              <button className="btn ghost sm" onClick={doRerun} disabled={rerun.isPending}>
                {rerun.isPending ? t("studio.view.regenerating") : t("studio.view.retryPhase")}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Badge de estado de fase
// ─────────────────────────────────────────────────────────────────────────────

export function PhaseStatusBadge({
  state,
  stepId,
  designStatus,
}: {
  state: PhaseState;
  stepId: string;
  designStatus: DesignStepStatus;
}) {
  const t = useT();
  const LABELS: Record<PhaseState, string> = {
    pending: t("studio.view.status.pending"),
    running: stepId === "handoff" ? t("studio.view.status.publishing") : t("studio.view.status.generating"),
    awaiting: t("studio.view.status.awaiting"),
    approved: t("studio.view.status.approved"),
    failed: t("studio.view.status.reviewing"),
  };
  const PILL_CLS: Record<PhaseState, string> = {
    pending: "queued",
    running: "run_",
    awaiting: "await",
    approved: "done",
    failed: "fail",
  };
  void designStatus; // se usa solo para derivar el label de running
  return <span className={`pill ${PILL_CLS[state]}`}>{LABELS[state]}</span>;
}

// ─────────────────────────────────────────────────────────────────────────────
// Renderizador de artefacto: MOCKUPS — iframe sandboxed con HTML autocontenido
// ─────────────────────────────────────────────────────────────────────────────

function MockupsArtifact({ html }: { html: string }) {
  const t = useT();
  function openInNewTab() {
    const blob = new Blob([html], { type: "text/html" });
    const url = URL.createObjectURL(blob);
    window.open(url, "_blank", "noopener,noreferrer");
    // Revocar el URL después de un momento para liberar memoria.
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <button className="btn ghost sm" onClick={openInNewTab}>
          ⛶ {t("studio.view.openNewTab")}
        </button>
      </div>
      <iframe
        srcDoc={html}
        sandbox="allow-scripts allow-forms"
        title={t("studio.view.mockupPreviewTitle")}
        style={{
          width: "100%",
          height: 700,
          border: "1px solid var(--stroke)",
          borderRadius: "var(--r)",
          background: "#fff",
        }}
      />
    </div>
  );
}
