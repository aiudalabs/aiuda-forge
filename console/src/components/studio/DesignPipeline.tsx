"use client";

// DesignPipeline (ex ProjectDetail) — el pipeline de un design run: stepper de
// fases + PhasePanel (artefacto + aprobar/rechazar) + relanzar/iterar/eliminar.
// Autocontenido: dado solo un runId, resuelve su propio useDesignRun y se
// renderiza sin depender de ningún estado del padre (C6b fase 2).

import { useEffect, useRef, useState } from "react";
import {
  useCreateDesignRun,
  useDeleteRun,
  useDesignRun,
} from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import { PhasePanel } from "./PhasePanel";
import { IterationModal } from "./StudioModals";
import { activePhaseIndex, phaseLabel, phaseState, PHASE_GLYPH_CLS, PHASE_ICON } from "./phaseHelpers";

export function DesignPipeline({
  runId,
  onNewRun,
  onDeleted,
}: {
  runId: string;
  /** Opcional: notifica al padre de un run nuevo (relanzamiento/iteración) para que
   *  navegue a él. Si se omite (uso standalone), el nuevo run queda RUNNING y
   *  useActiveDesignRun lo recoge solo en el próximo render. */
  onNewRun?: (id: string) => void;
  /** Opcional: notifica al padre que este run se borró (para que navegue a otro).
   *  Si se omite, el panel se oculta localmente tras el borrado. */
  onDeleted?: () => void;
}) {
  const t = useT();
  const { data: run, isLoading } = useDesignRun(runId);
  const [selectedPhaseIdx, setSelectedPhaseIdx] = useState<number | null>(null);
  const createDesignRun = useCreateDesignRun();
  const deleteRun = useDeleteRun();
  const [relaunching, setRelaunching] = useState(false);
  const [showIteration, setShowIteration] = useState(false);
  const [deleted, setDeleted] = useState(false);

  function doDelete() {
    if (!window.confirm(t("studio.view.deleteRunConfirm"))) return;
    deleteRun.mutate([runId], {
      onSuccess: () => {
        if (onDeleted) onDeleted();
        else setDeleted(true); // sin padre que navegue: ocultar el panel localmente
      },
    });
  }

  // Al cambiar de run, o cuando los datos llegan, reseteamos al paso activo.
  const prevRunId = useRef<string | null>(null);
  useEffect(() => {
    if (prevRunId.current !== runId) {
      prevRunId.current = runId;
      setSelectedPhaseIdx(null);
    }
  }, [runId]);

  if (deleted) return null;

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
  const isTerminal = run.status === "DONE" || run.status === "FAILED" || run.status === "CANCELLED";

  // Un proyecto "ya publicado" es el que llegó al handoff aprobado (DONE): su
  // backlog está en el ticket store y el producto tiene un repo con historias.
  // Para esos, "seguir desarrollando" = una ITERACIÓN (delta), no rehacer el
  // diseño completo con la idea original. El relanzamiento de diseño se reserva
  // para runs que NO publicaron (donde volver a diseñar sí tiene sentido).
  const publishedBacklog = phases.some(
    (p) => p.stepId === "handoff" && phaseState(p) === "approved"
  );

  async function doRelaunch() {
    if (!run) return;
    setRelaunching(true);
    try {
      const next = await createDesignRun.mutateAsync({
        project_id: run.project_id ?? "",
        repo: run.repo ?? "",
        instructions: run.idea ?? "",
      });
      // Sin padre que navegue, el nuevo run queda RUNNING y useActiveDesignRun lo
      // recoge solo en el próximo render.
      onNewRun?.(next.id);
    } finally {
      setRelaunching(false);
    }
  }

  return (
    <div className="studio-detail">
      {/* Barra de acciones del run: eliminar este run de diseño/iteración (no borra
          el proyecto ni el repo). Visible para cualquier run. */}
      <div style={{ display: "flex", justifyContent: "flex-end", marginBottom: 6 }}>
        <button
          className="btn ghost sm"
          style={{ color: "var(--danger)" }}
          onClick={doDelete}
          disabled={deleteRun.isPending}
          title={t("studio.view.deleteRunTitle")}
        >
          {deleteRun.isPending ? t("studio.view.deletingRun") : t("studio.view.deleteRun")}
        </button>
      </div>

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
              title={phaseLabel(t, p.stepId, p.name)}
            >
              <span className="ps-icon">{PHASE_ICON[st]}</span>
              <span className="ps-name">{phaseLabel(t, p.stepId, p.name)}</span>
            </button>
          );
        })}
      </div>

      {/* Run terminal: iterar (si ya publicó backlog) o relanzar el diseño. */}
      {isTerminal && (
        <div style={{ display: "flex", justifyContent: "flex-end", margin: "8px 0" }}>
          {publishedBacklog ? (
            <button className="btn primary sm" onClick={() => setShowIteration(true)}>
              {t("studio.view.newIteration")}
            </button>
          ) : (
            <button className="btn ghost sm" onClick={doRelaunch} disabled={relaunching}>
              {relaunching ? t("studio.view.relaunching") : t("studio.view.relaunchDesign")}
            </button>
          )}
        </div>
      )}

      {/* Cuerpo: artefacto + acciones */}
      {viewPhase && (
        <PhasePanel
          runId={run.id}
          phase={viewPhase}
          state={viewState}
        />
      )}

      {/* Modal: nueva iteración (change request → workflow iterate) */}
      <div
        className={`overlay ${showIteration ? "on" : ""}`}
        onClick={() => setShowIteration(false)}
      />
      {showIteration && (
        <IterationModal
          projectId={run.project_id ?? ""}
          repo={run.repo ?? ""}
          onClose={() => setShowIteration(false)}
          onCreated={(id) => {
            setShowIteration(false);
            onNewRun?.(id);
          }}
        />
      )}
    </div>
  );
}
