"use client";

// STUDIO — plano de diseño (doc 16 §2.4). Vista real sobre runs del workflow "design":
// lista de proyectos, pipeline de fases, visor de artefactos, aprobar/rechazar por fase.
// Backend: GET/POST /runs (filtrado por workflow_id="design"), GET /runs/{id},
// GET /runs/{id}/artifacts/{stepId}, POST /runs/{id}/steps/{gateStepId}/approve|reject.

import { useState } from "react";
import {
  useDesignRunSummary,
  useDesignRuns,
  useProjects,
} from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import type { DesignRun, Project } from "@/lib/types";
import { DesignPipeline } from "./DesignPipeline";
import { NewProjectModal } from "./StudioModals";
import { approvedCount, activePhaseIndex, phaseLabel, phaseState } from "./phaseHelpers";

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

  // Ciclo de diseño por run: un proyecto puede tener varios design runs
  // (relanzamientos / iteraciones). Se numeran por antigüedad dentro del
  // proyecto para que dos cards del mismo proyecto sean distinguibles.
  const cycleOf = new Map<string, { n: number; of: number }>();
  {
    const byProject = new Map<string, DesignRun[]>();
    for (const r of list) {
      const key = r.project_id ?? `run:${r.id}`;
      const arr = byProject.get(key) ?? [];
      arr.push(r);
      byProject.set(key, arr);
    }
    for (const arr of byProject.values()) {
      arr
        .slice()
        .sort((a, b) => a.created_at - b.created_at)
        .forEach((r, i) => cycleOf.set(r.id, { n: i + 1, of: arr.length }));
    }
  }

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
                cycle={cycleOf.get(run.id)}
                active={run.id === effectiveSel}
                onSelect={() => setSelectedRunId(run.id)}
              />
            ))}
          </div>

          {/* Panel derecho: detalle del proyecto seleccionado */}
          <div className="studio-main">
            {effectiveSel ? (
              <DesignPipeline
                runId={effectiveSel}
                onNewRun={(id) => setSelectedRunId(id)}
                onDeleted={() => {
                  // Tras borrar, salta al siguiente run de la lista (o a ninguno).
                  const next = list.find((r) => r.id !== effectiveSel);
                  setSelectedRunId(next ? next.id : null);
                }}
              />
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
  cycle,
  active,
  onSelect,
}: {
  run: DesignRun;
  project?: Project;
  cycle?: { n: number; of: number };
  active: boolean;
  onSelect: () => void;
}) {
  const t = useT();
  // GET /runs no trae steps → run.phases de la lista viene todo QUEUED ("0/7"
  // eterno). El progreso real vive en el detalle del run; sondeo lento con
  // cache compartida y run.phases como fallback mientras carga.
  const { data: detailed } = useDesignRunSummary(run.id);
  const phases = detailed?.phases ?? run.phases;
  const done = approvedCount(phases);
  const total = phases.length;
  const activeIdx = activePhaseIndex(phases);
  const curPhase = phases[activeIdx];
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
      <div className="proj-name">
        {displayName}
        {(run.workflow_id === "iterate" || (cycle && cycle.of > 1)) && (
          <span className="proj-cycle">
            {" · "}
            {run.workflow_id === "iterate"
              ? t("studio.view.iteration")
              : t("studio.view.cycle", { n: cycle!.n })}
            {" · "}
            {new Date(run.created_at).toLocaleDateString(undefined, {
              day: "numeric",
              month: "short",
            })}
          </span>
        )}
      </div>
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
          <span className="proj-cur">{phaseLabel(t, curPhase.stepId, curPhase.name)}</span>
        )}
      </div>
    </button>
  );
}
