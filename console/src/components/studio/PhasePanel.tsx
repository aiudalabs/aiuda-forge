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
  useReject,
  useAnswer,
  useLiveEvents,
} from "@/lib/hooks";
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
  // El live-log de la generación ya NO vive aquí: se movió al drawer de Actividad
  // al pie del workspace (StudioDocs), para no duplicar la terminal ni encajonarla
  // dentro del panel de la fase. Aquí solo indicamos "generando…".

  const {
    data: artifactText,
    isLoading: artLoading,
    isError: artError,
  } = useArtifact(runId, hasArtifact ? phase.stepId : null);

  const approve = useApprove();
  const reject = useReject();
  const answer = useAnswer();
  const rerun = useRerunStep();
  // formMode selects which inline form is open — mutually exclusive with the
  // default three-button row (Responder / Rechazar / Aprobar).
  const [formMode, setFormMode] = useState<null | "reject" | "answer">(null);
  const [rejectInput, setRejectInput] = useState("");
  const [answerInput, setAnswerInput] = useState("");

  const gateStepId = phase.gateId || `${phase.stepId}_gate`;

  // Q&A history for THIS phase: the run's step.answer events targeting this gate.
  // The run stays RUNNING while a gate is parked, so the live events cover it; the
  // hook also loads the full history on mount, so past answers show even when idle.
  const events = useLiveEvents(runId, state === "awaiting" || state === "running");
  const qa = events.filter(
    (e) => e.type === "step.answer" && (e.data?.step as string | undefined) === gateStepId,
  );

  const busy = approve.isPending || reject.isPending || answer.isPending;

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
        setFormMode(null);
        setRejectInput("");
      },
    });
  }

  function doAnswer() {
    if (!answerInput.trim()) return;
    answer.mutate([runId, gateStepId, answerInput.trim()], {
      onSuccess: () => {
        setFormMode(null);
        setAnswerInput("");
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
              <span style={{ display: "inline-flex", alignItems: "center", gap: 8, color: "var(--ink2)" }}>
                <span className="spin" style={{ width: 14, height: 14 }} /> {t("studio.view.generatingDoc")}
              </span>
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

      {/* Q&A de la fase: respuestas ya enviadas a las open questions (step.answer). */}
      {qa.length > 0 && (
        <div className="phase-qa">
          <div className="phase-qa-title">{t("studio.view.qaHistory")}</div>
          <ul className="phase-qa-list">
            {qa.map((e, i) => (
              <li key={e.id ?? i} className="phase-qa-item">
                {String(e.data?.text ?? "")}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Acciones: solo cuando el gate está AWAITING */}
      {state === "awaiting" && phase.stepId !== "handoff" && (
        <div className="phase-actions">
          {formMode === null && (
            <>
              <button
                className="btn ghost"
                onClick={() => setFormMode("answer")}
                disabled={busy}
              >
                {t("studio.view.answer")}
              </button>
              <button
                className="btn ghost"
                onClick={() => setFormMode("reject")}
                disabled={busy}
              >
                {t("studio.view.reject")}
              </button>
              <button
                className="btn primary"
                onClick={doApprove}
                disabled={busy}
              >
                {approve.isPending ? t("studio.view.approving") : t("studio.view.approve")}
              </button>
            </>
          )}
          {formMode === "answer" && (
            <div className="reject-form">
              <textarea
                className="inp"
                style={{ resize: "vertical", minHeight: 72, fontSize: 13 }}
                placeholder={t("studio.view.answerPlaceholder")}
                value={answerInput}
                onChange={(e) => setAnswerInput(e.target.value)}
                autoFocus
              />
              <div style={{ display: "flex", gap: 8 }}>
                <button
                  className="btn ghost sm"
                  onClick={() => {
                    setFormMode(null);
                    setAnswerInput("");
                  }}
                >
                  {t("studio.view.cancel")}
                </button>
                <button
                  className="btn primary sm"
                  onClick={doAnswer}
                  disabled={!answerInput.trim() || answer.isPending}
                >
                  {answer.isPending ? t("studio.view.answering") : t("studio.view.sendAnswer")}
                </button>
              </div>
            </div>
          )}
          {formMode === "reject" && (
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
                    setFormMode(null);
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
