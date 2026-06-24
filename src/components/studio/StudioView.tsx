"use client";

// STUDIO — plano de diseño (doc 16 §2.4). Vista real sobre runs del workflow "design":
// lista de proyectos, pipeline de fases, visor de artefactos, aprobar/rechazar por fase.
// Backend: GET/POST /runs (filtrado por workflow_id="design"), GET /runs/{id},
// GET /runs/{id}/artifacts/{stepId}, POST /runs/{id}/steps/{gateStepId}/approve|reject.

import { useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import {
  useArtifact,
  useApprove,
  useCreateDesignRun,
  useDesignRun,
  useDesignRuns,
  useReject,
} from "@/lib/hooks";
import type { DesignPhase, DesignRun, DesignStepStatus } from "@/lib/types";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers de fase
// ─────────────────────────────────────────────────────────────────────────────

// Estado compuesto de una fase: derivado del estado del paso de diseño y del gate.
type PhaseState = "pending" | "running" | "awaiting" | "approved" | "failed";

function phaseState(p: DesignPhase): PhaseState {
  if (p.gateStatus === "DONE") return "approved";
  if (p.gateStatus === "FAILED") return "failed";
  if (p.gateStatus === "AWAITING") return "awaiting";
  if (p.designStatus === "RUNNING") return "running";
  if (p.designStatus === "DONE" && p.gateStatus === "QUEUED") return "running"; // gate pendiente
  return "pending";
}

const PHASE_ICON: Record<PhaseState, string> = {
  pending: "◌",
  running: "●",
  awaiting: "⏸",
  approved: "✓",
  failed: "✕",
};

// Mapeo de estado a clase CSS del glifo en el stepper horizontal.
const PHASE_GLYPH_CLS: Record<PhaseState, string> = {
  pending: "ph-pend",
  running: "ph-run",
  awaiting: "ph-await",
  approved: "ph-ok",
  failed: "ph-fail",
};

// Cuántas fases están aprobadas (para el indicador de progreso).
function approvedCount(phases: DesignPhase[]): number {
  return phases.filter((p) => phaseState(p) === "approved").length;
}

// Fase activa: la primera que no está aprobada.
function activePhaseIndex(phases: DesignPhase[]): number {
  const idx = phases.findIndex((p) => phaseState(p) !== "approved");
  return idx === -1 ? phases.length - 1 : idx;
}

// Pasos con artefacto visible (los que producen un doc).
const ARTIFACT_STEPS = new Set(["discovery", "prd", "architecture", "ui", "backlog", "handoff"]);

// ─────────────────────────────────────────────────────────────────────────────
// Raíz
// ─────────────────────────────────────────────────────────────────────────────

export function StudioView() {
  const { data: runs, isLoading, isError } = useDesignRuns();
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [showNewProject, setShowNewProject] = useState(false);

  // Al recibir la lista, selecciona automáticamente el primer run si ninguno está seleccionado.
  const list = runs ?? [];
  const effectiveSel = selectedRunId ?? list[0]?.id ?? null;

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>Studio</h2>
        <span className="c">diseño guiado · fase por fase</span>
        <span className="sp" />
        <button className="btn ghost sm" onClick={() => setShowNewProject(true)}>
          + Nuevo proyecto
        </button>
      </div>

      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic">
            <span className="spin" />
          </div>
          Cargando proyectos…
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          No se pudo conectar al control-plane.
        </div>
      ) : list.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">✦</div>
          Sin proyectos de diseño todavía.{" "}
          <button className="btn ghost sm" onClick={() => setShowNewProject(true)}>
            Crear el primero
          </button>
        </div>
      ) : (
        <div className="studio-layout">
          {/* Columna izquierda: lista de proyectos */}
          <div className="studio-sidebar">
            <div className="eyebrow" style={{ marginBottom: 10 }}>
              Proyectos
            </div>
            {list.map((run) => (
              <ProjectCard
                key={run.id}
                run={run}
                active={run.id === effectiveSel}
                onSelect={() => setSelectedRunId(run.id)}
              />
            ))}
          </div>

          {/* Panel derecho: detalle del proyecto seleccionado */}
          <div className="studio-main">
            {effectiveSel ? (
              <ProjectDetail runId={effectiveSel} />
            ) : (
              <div className="placeholder">Selecciona un proyecto.</div>
            )}
          </div>
        </div>
      )}

      {/* Modal: nuevo proyecto */}
      <div
        className={`overlay ${showNewProject ? "on" : ""}`}
        onClick={() => setShowNewProject(false)}
      />
      {showNewProject && (
        <NewProjectModal
          onClose={() => setShowNewProject(false)}
          onCreated={(id) => {
            setSelectedRunId(id);
            setShowNewProject(false);
          }}
        />
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Tarjeta de proyecto en la lista lateral
// ─────────────────────────────────────────────────────────────────────────────

function ProjectCard({
  run,
  active,
  onSelect,
}: {
  run: DesignRun;
  active: boolean;
  onSelect: () => void;
}) {
  const done = approvedCount(run.phases);
  const total = run.phases.length;
  const activeIdx = activePhaseIndex(run.phases);
  const curPhase = run.phases[activeIdx];
  const state = curPhase ? phaseState(curPhase) : "approved";

  return (
    <button
      className={`proj-card${active ? " active" : ""}`}
      onClick={onSelect}
      aria-pressed={active}
    >
      <div className="proj-idea">{run.idea}</div>
      <div className="proj-meta">
        <span className={`proj-dot ${state}`} />
        <span className="proj-progress">
          {done}/{total} fases
        </span>
        {curPhase && state !== "approved" && (
          <span className="proj-cur">{curPhase.name}</span>
        )}
      </div>
    </button>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Detalle del proyecto: stepper + visor de artefacto + acciones
// ─────────────────────────────────────────────────────────────────────────────

function ProjectDetail({ runId }: { runId: string }) {
  const { data: run, isLoading } = useDesignRun(runId);
  const [selectedPhaseIdx, setSelectedPhaseIdx] = useState<number | null>(null);

  // Al cambiar de run, o cuando los datos llegan, reseteamos al paso activo.
  const prevRunId = useRef<string | null>(null);
  useEffect(() => {
    if (prevRunId.current !== runId) {
      prevRunId.current = runId;
      setSelectedPhaseIdx(null);
    }
  }, [runId]);

  if (isLoading || !run) {
    return (
      <div className="placeholder">
        <div className="ph-ic">
          <span className="spin" />
        </div>
        Cargando proyecto…
      </div>
    );
  }

  const phases = run.phases;
  const activeIdx = activePhaseIndex(phases);
  const viewIdx = selectedPhaseIdx ?? activeIdx;
  const viewPhase = phases[viewIdx];
  const viewState = viewPhase ? phaseState(viewPhase) : "pending";

  return (
    <div className="studio-detail">
      {/* Stepper horizontal de fases */}
      <div className="phase-stepper">
        {phases.map((p, i) => {
          const st = phaseState(p);
          const isView = i === viewIdx;
          return (
            <button
              key={p.stepId}
              className={`phase-step${isView ? " active" : ""} ${PHASE_GLYPH_CLS[st]}`}
              onClick={() => setSelectedPhaseIdx(i)}
              title={p.name}
            >
              <span className="ps-icon">{PHASE_ICON[st]}</span>
              <span className="ps-name">{p.name}</span>
            </button>
          );
        })}
      </div>

      {/* Cuerpo: artefacto + acciones */}
      {viewPhase && (
        <PhasePanel
          runId={run.id}
          phase={viewPhase}
          state={viewState}
        />
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Panel de una fase: artefacto (markdown) + aprobar/rechazar
// ─────────────────────────────────────────────────────────────────────────────

function PhasePanel({
  runId,
  phase,
  state,
}: {
  runId: string;
  phase: DesignPhase;
  state: PhaseState;
}) {
  const hasArtifact =
    ARTIFACT_STEPS.has(phase.stepId) && phase.designStatus === "DONE";

  const {
    data: artifactText,
    isLoading: artLoading,
    isError: artError,
  } = useArtifact(runId, hasArtifact ? phase.stepId : null);

  const approve = useApprove();
  const reject = useReject();
  const [rejectInput, setRejectInput] = useState("");
  const [showRejectForm, setShowRejectForm] = useState(false);

  const gateStepId = `${phase.stepId}_gate`;

  function doApprove() {
    approve.mutate([runId, gateStepId]);
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
          <h3 className="phase-panel-title">{phase.name}</h3>
          <PhaseStatusBadge state={state} stepId={phase.stepId} designStatus={phase.designStatus} />
        </div>
      </div>

      {/* Artefacto */}
      <div className="artifact-box">
        {!hasArtifact && (
          <div className="artifact-empty">
            {phase.designStatus === "RUNNING" ? (
              <span><span className="spin" style={{ width: 14, height: 14 }} /> Generando documento…</span>
            ) : (
              <span style={{ color: "var(--ink4)" }}>El documento se mostrará aquí cuando la fase complete.</span>
            )}
          </div>
        )}

        {hasArtifact && artLoading && (
          <div className="artifact-empty">
            <span className="spin" style={{ width: 14, height: 14 }} /> Cargando artefacto…
          </div>
        )}

        {hasArtifact && artError && (
          <div className="artifact-empty" style={{ color: "var(--accent)" }}>
            No se pudo cargar el documento.
          </div>
        )}

        {hasArtifact && artifactText && (
          <div className="artifact-md">
            <ReactMarkdown>{artifactText}</ReactMarkdown>
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
                Rechazar / pedir cambios
              </button>
              <button
                className="btn primary"
                onClick={doApprove}
                disabled={approve.isPending || reject.isPending}
              >
                {approve.isPending ? "Aprobando…" : "Aprobar fase"}
              </button>
            </>
          ) : (
            <div className="reject-form">
              <textarea
                className="inp"
                style={{ resize: "vertical", minHeight: 72, fontSize: 13 }}
                placeholder="Describe los cambios que necesitas (el agente usará esto como feedback)…"
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
                  Cancelar
                </button>
                <button
                  className="btn primary sm"
                  onClick={doReject}
                  disabled={!rejectInput.trim() || reject.isPending}
                >
                  {reject.isPending ? "Enviando…" : "Enviar feedback"}
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Estado final: aprobada o fallida */}
      {state === "approved" && (
        <div className="phase-banner ok">
          Fase aprobada
        </div>
      )}
      {state === "failed" && (
        <div className="phase-banner fail">
          Fase rechazada — el agente está revisando el feedback.
        </div>
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Badge de estado de fase
// ─────────────────────────────────────────────────────────────────────────────

function PhaseStatusBadge({
  state,
  stepId,
  designStatus,
}: {
  state: PhaseState;
  stepId: string;
  designStatus: DesignStepStatus;
}) {
  const LABELS: Record<PhaseState, string> = {
    pending: "pendiente",
    running: stepId === "handoff" ? "publicando" : "generando",
    awaiting: "esperando aprobación",
    approved: "aprobada",
    failed: "revisando",
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
// Modal: nuevo proyecto
// ─────────────────────────────────────────────────────────────────────────────

function NewProjectModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (id: string) => void;
}) {
  const [idea, setIdea] = useState("");
  const [error, setError] = useState<string | null>(null);
  const create = useCreateDesignRun();

  // Cerrar con Escape.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const text = idea.trim();
    if (!text) {
      setError("Escribe una descripción del producto.");
      return;
    }
    try {
      const run = await create.mutateAsync(text);
      onCreated(run.id);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="modal on" role="dialog" aria-modal="true" aria-labelledby="np-title">
      <div className="mh">
        <h3 id="np-title">Nuevo proyecto de diseño</h3>
        <button className="x" onClick={onClose} aria-label="Cerrar">
          ✕
        </button>
      </div>
      <form className="mb" onSubmit={handleSubmit}>
        <div className="field">
          <label htmlFor="np-idea">Idea del producto</label>
          <textarea
            id="np-idea"
            className="inp"
            style={{ resize: "vertical", minHeight: 96 }}
            value={idea}
            onChange={(e) => setIdea(e.target.value)}
            placeholder="Describe tu producto en 1-3 frases: qué problema resuelve, para quién, qué lo hace diferente."
            autoFocus
            required
          />
        </div>

        {error && (
          <div
            style={{
              padding: "8px 12px",
              background: "var(--accent-soft)",
              border: "1px solid var(--accent-line)",
              borderRadius: 4,
              fontSize: 12,
              color: "var(--accent)",
              marginBottom: 10,
            }}
          >
            {error}
          </div>
        )}

        <div style={{ display: "flex", gap: 10 }}>
          <button type="button" className="btn ghost" style={{ flex: 1 }} onClick={onClose}>
            Cancelar
          </button>
          <button
            type="submit"
            className="btn primary"
            style={{ flex: 1 }}
            disabled={create.isPending}
          >
            {create.isPending ? "Creando…" : "Iniciar diseño"}
          </button>
        </div>
      </form>
    </div>
  );
}
