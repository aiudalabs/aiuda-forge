"use client";

// STUDIO — plano de diseño (doc 16 §2.4). Vista real sobre runs del workflow "design":
// lista de proyectos, pipeline de fases, visor de artefactos, aprobar/rechazar por fase.
// Backend: GET/POST /runs (filtrado por workflow_id="design"), GET /runs/{id},
// GET /runs/{id}/artifacts/{stepId}, POST /runs/{id}/steps/{gateStepId}/approve|reject.

import { useEffect, useRef, useState } from "react";
import * as jsYaml from "js-yaml";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  useArtifact,
  useApprove,
  useCreateDesignRun,
  useCreateProject,
  useDesignRun,
  useDesignRuns,
  useProjects,
  useReject,
} from "@/lib/hooks";
import { useActiveProject } from "@/lib/activeProject";
import { ApiError } from "@/lib/api";
import { useT } from "@/lib/i18n";
import type { DesignPhase, DesignRun, DesignStepStatus, Project } from "@/lib/types";

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
const ARTIFACT_STEPS = new Set(["discovery", "prd", "architecture", "ui", "mockups", "backlog", "handoff"]);

// ─────────────────────────────────────────────────────────────────────────────
// Raíz
// ─────────────────────────────────────────────────────────────────────────────

export function StudioView() {
  const t = useT();
  const { data: runs, isLoading, isError } = useDesignRuns();
  const { data: projects } = useProjects();
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [showNewProject, setShowNewProject] = useState(false);

  // Al recibir la lista, selecciona automáticamente el primer run si ninguno está seleccionado.
  const list = runs ?? [];
  const effectiveSel = selectedRunId ?? list[0]?.id ?? null;

  // Índice de proyectos por id para resolver nombre/repo desde el run.
  const projectById = new Map<string, Project>(
    (projects ?? []).map((p) => [p.id, p])
  );

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>{t("studio.view.title")}</h2>
        <span className="c">{t("studio.view.subtitle")}</span>
        <span className="sp" />
        <button className="btn ghost sm" onClick={() => setShowNewProject(true)}>
          {t("studio.view.newProject")}
        </button>
      </div>

      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic">
            <span className="spin" />
          </div>
          {t("studio.view.loadingProjects")}
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("studio.view.connectError")}
        </div>
      ) : list.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">✦</div>
          {t("studio.view.noProjects")}{" "}
          <button className="btn ghost sm" onClick={() => setShowNewProject(true)}>
            {t("studio.view.createFirst")}
          </button>
        </div>
      ) : (
        <div className="studio-layout">
          {/* Columna izquierda: lista de proyectos */}
          <div className="studio-sidebar">
            <div className="eyebrow" style={{ marginBottom: 10 }}>
              {t("studio.view.projects")}
            </div>
            {list.map((run) => (
              <ProjectCard
                key={run.id}
                run={run}
                project={run.project_id ? projectById.get(run.project_id) : undefined}
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
              <div className="placeholder">{t("studio.view.selectProject")}</div>
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
  project,
  active,
  onSelect,
}: {
  run: DesignRun;
  project?: Project;
  active: boolean;
  onSelect: () => void;
}) {
  const t = useT();
  const done = approvedCount(run.phases);
  const total = run.phases.length;
  const activeIdx = activePhaseIndex(run.phases);
  const curPhase = run.phases[activeIdx];
  const state = curPhase ? phaseState(curPhase) : "approved";

  // Nombre del proyecto: del Project si está vinculado, si no la primera palabra del idea.
  const displayName =
    project?.name ??
    (run.idea.split(" ").slice(0, 4).join(" ") + (run.idea.split(" ").length > 4 ? "…" : ""));

  // Etiqueta corta del repo: "owner/repo" extraída de la URL https.
  const repoLabel = (run.repo ?? project?.repo ?? "")
    .replace(/^https?:\/\/[^/]+\//, "")
    .replace(/\.git$/, "");
  const repoHref = run.repo ?? project?.repo ?? "";

  return (
    <button
      className={`proj-card${active ? " active" : ""}`}
      onClick={onSelect}
      aria-pressed={active}
    >
      <div className="proj-name">{displayName}</div>
      {repoLabel && (
        <a
          className="proj-repo"
          href={repoHref}
          target="_blank"
          rel="noopener noreferrer"
          onClick={(e) => e.stopPropagation()}
        >
          ↗ {repoLabel}
        </a>
      )}
      <div className="proj-meta">
        <span className={`proj-dot ${state}`} />
        <span className="proj-progress">
          {t("studio.view.phasesProgress", { done, total })}
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
  const t = useT();
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
        {t("studio.view.loadingProject")}
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
  const t = useT();
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

  const gateStepId = phase.gateId || `${phase.stepId}_gate`;

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
              <span><span className="spin" style={{ width: 14, height: 14 }} /> {t("studio.view.generatingDoc")}</span>
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

        {hasArtifact && artifactText && phase.stepId === "backlog" && (
          <BacklogArtifact raw={artifactText} />
        )}

        {hasArtifact && artifactText && phase.stepId !== "mockups" && phase.stepId !== "backlog" && (
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
      {state === "failed" && (
        <div className="phase-banner fail">
          {t("studio.view.phaseRejected")}
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
      <iframe
        srcDoc={html}
        sandbox="allow-same-origin"
        title={t("studio.view.mockupPreviewTitle")}
        style={{
          width: "100%",
          height: 480,
          border: "1px solid var(--stroke)",
          borderRadius: "var(--r)",
          background: "#fff",
        }}
      />
      <div style={{ display: "flex", justifyContent: "flex-end" }}>
        <button className="btn ghost sm" onClick={openInNewTab}>
          {t("studio.view.openNewTab")}
        </button>
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Tipos internos para el parser YAML del backlog
// ─────────────────────────────────────────────────────────────────────────────

interface BacklogStory {
  id?: string;
  title?: string;
  body?: string;
  acceptance?: string;
  owner?: string;
  deps?: string[];
}

interface BacklogYaml {
  epic?: {
    id?: string;
    title?: string;
    description?: string;
  };
  stories?: BacklogStory[];
}

// ─────────────────────────────────────────────────────────────────────────────
// Renderizador de artefacto: BACKLOG — tarjetas de historia legibles desde YAML
// ─────────────────────────────────────────────────────────────────────────────

function BacklogArtifact({ raw }: { raw: string }) {
  const t = useT();
  let parsed: BacklogYaml | null = null;
  try {
    parsed = jsYaml.load(raw) as BacklogYaml;
  } catch {
    /* YAML inválido — caemos al markdown */
  }

  // Si el parse falla o el doc no tiene la forma esperada, renderizamos como markdown.
  if (!parsed || (!parsed.epic && !parsed.stories)) {
    return (
      <div className="artifact-md">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{raw}</ReactMarkdown>
      </div>
    );
  }

  const { epic, stories = [] } = parsed;

  return (
    <div className="backlog-artifact">
      {epic && (
        <div className="backlog-epic">
          <div className="backlog-epic-id">{epic.id}</div>
          <h3 className="backlog-epic-title">{epic.title}</h3>
          {epic.description && (
            <p className="backlog-epic-desc">{epic.description}</p>
          )}
        </div>
      )}

      <div className="story-list">
        {stories.map((s, i) => (
          <div key={s.id ?? i} className="story-card">
            <div className="story-card-header">
              <span className="story-id">{s.id}</span>
              <span className="story-title">{s.title}</span>
            </div>
            <div className="story-card-meta">
              {s.owner && (
                <span className="story-chip owner">{s.owner}</span>
              )}
              {(s.deps ?? []).map((d) => (
                <span key={d} className="story-chip dep">
                  dep: {d}
                </span>
              ))}
            </div>
            {(s.body || s.acceptance) && (
              <div className="story-card-body">
                {s.body && (
                  <div className="artifact-md" style={{ padding: 0 }}>
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>{s.body}</ReactMarkdown>
                  </div>
                )}
                {s.acceptance && (
                  <>
                    <div className="story-acceptance-label">{t("studio.view.acceptanceCriteria")}</div>
                    <div className="artifact-md" style={{ padding: 0 }}>
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>{s.acceptance}</ReactMarkdown>
                    </div>
                  </>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
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
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const createProject = useCreateProject();
  const createDesignRun = useCreateDesignRun();
  const { setActiveId } = useActiveProject();
  const t = useT();

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
    const trimmedName = name.trim();
    const trimmedDesc = description.trim();
    if (!trimmedName) {
      setError(t("studio.modal.validation.name"));
      return;
    }
    if (!trimmedDesc) {
      setError(t("studio.modal.validation.desc"));
      return;
    }
    setBusy(true);
    try {
      // 1. Crear el proyecto (crea el repo en GitHub).
      const proj = await createProject.mutateAsync({ name: trimmedName, description: trimmedDesc });
      // El proyecto recién creado pasa a ser el activo: la consola se scopea a él.
      setActiveId(proj.id);
      // 2. Iniciar el run de diseño vinculado al proyecto.
      const run = await createDesignRun.mutateAsync({
        project_id: proj.id,
        repo: proj.repo,
        instructions: trimmedDesc,
      });
      onCreated(run.id);
    } catch (err: unknown) {
      // 409 = repo ya existe — lo mostramos inline de forma más amable.
      if (err instanceof ApiError && err.status === 409) {
        setError(t("studio.modal.repoExists", { name: trimmedName }));
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="modal on" role="dialog" aria-modal="true" aria-labelledby="np-title">
      <div className="mh">
        <h3 id="np-title">{t("studio.modal.title")}</h3>
        <button className="x" onClick={onClose} aria-label={t("studio.modal.close")}>
          ✕
        </button>
      </div>
      <form className="mb" onSubmit={handleSubmit}>
        <div className="field">
          <label htmlFor="np-name">{t("studio.modal.nameLabel")}</label>
          <input
            id="np-name"
            type="text"
            className="inp"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("studio.modal.namePlaceholder")}
            autoFocus
            required
          />
        </div>
        <div className="field">
          <label htmlFor="np-desc">{t("studio.modal.descLabel")}</label>
          <textarea
            id="np-desc"
            className="inp"
            style={{ resize: "vertical", minHeight: 96 }}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder={t("studio.modal.descPlaceholder")}
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
            {t("studio.modal.cancel")}
          </button>
          <button
            type="submit"
            className="btn primary"
            style={{ flex: 1 }}
            disabled={busy}
          >
            {busy ? t("studio.modal.creating") : t("studio.modal.create")}
          </button>
        </div>
      </form>
    </div>
  );
}
