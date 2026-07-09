"use client";

// FlowView — el proyecto ENTERO como un grafo interactivo en vivo: el pipeline de
// diseño con sus gates, el handoff, y los sprints con sus stories/deps y las tres
// ceremonias intercaladas donde la derivación las pone. Click en un nodo = ir a su
// artefacto (doc de fase, ticket, run de ceremonia, preview). Calca DepGraph
// (React Flow v12 + dagre) y REUSA la maquinaria del Studio/Board (phaseState,
// PhasePanel, TicketDetail, RunDrawer) — no la reimplementa.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  useReactFlow,
  type Node,
  type Edge,
  type Viewport,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";

import { useActiveProjectId } from "@/lib/activeProject";
import {
  useDesignRuns,
  useDesignRun,
  useTickets,
  useSprints,
  useProjectSettings,
  useMintPreviewToken,
} from "@/lib/hooks";
import { subscribe } from "@/lib/ws";
import { useQueryClient } from "@tanstack/react-query";
import { useT } from "@/lib/i18n";
import { STATUS_ORDER, statusToken } from "@/lib/statusToken";
import { phaseState } from "@/components/studio/phaseHelpers";
import { PhasePanel } from "@/components/studio/PhasePanel";
import { TicketDetail } from "@/components/tickets/TicketDetail";
import { RunDrawer } from "@/components/board/RunDrawer";
import type { DesignRun } from "@/lib/types";

import { buildFlow, type CeremonyModes } from "./flowGraph";
import { layoutFlow } from "./layout";
import { NODE_TYPES } from "./nodes";

// El run de diseño del proyecto: el workflow "design" (ciclo completo) más reciente;
// si no hay, el "iterate" más nuevo (delta). El detalle (con steps) trae las fases
// con estado real — el listado /runs no incluye steps.
function pickDesignRun(runs: DesignRun[] | undefined, projectId: string | null): DesignRun | null {
  const mine = (runs ?? []).filter((r) => r.project_id === projectId);
  if (mine.length === 0) return null;
  const byNewest = [...mine].sort((a, b) => b.created_at - a.created_at);
  return byNewest.find((r) => r.workflow_id === "design") ?? byNewest[0];
}

function minimapColor(n: Node): string {
  const d = n.data as { kind?: string; status?: string; state?: string };
  if (d.kind === "story" && d.status) return statusToken(d.status as never).border;
  if ((d.kind === "phase" || d.kind === "gate") && d.state === "running") return "var(--navy)";
  if (d.kind === "ceremony") return "var(--navy)";
  return "rgba(13,13,15,0.18)";
}

function FlowInner() {
  const t = useT();
  const router = useRouter();
  const qc = useQueryClient();
  const projectId = useActiveProjectId();

  const { data: designRuns } = useDesignRuns();
  const picked = useMemo(() => pickDesignRun(designRuns, projectId), [designRuns, projectId]);
  const { data: designRun } = useDesignRun(picked?.id ?? null);
  const { data: tickets } = useTickets(projectId);
  const { data: sprints } = useSprints();
  const { data: settings } = useProjectSettings(projectId);

  const [fullscreen, setFullscreen] = useState(false);
  const [sel, setSel] = useState<string | null>(null);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const { fitView, setViewport } = useReactFlow();
  const hydrated = useRef(false);
  const savedVp = useRef<Viewport | null>(null);

  // Push del bus: refresca el design run en sus PROPIOS eventos de step (el
  // invalidador global cubre tickets/runs, no ["designRun", id]). Así las fases/gates
  // reaccionan a step.status/step.answer sin esperar el poll de 3s.
  useEffect(() => {
    const id = picked?.id;
    if (!id) return;
    const off = subscribe((ev) => {
      if (ev.runId === id) qc.invalidateQueries({ queryKey: ["designRun", id] });
    });
    return off;
  }, [picked?.id, qc]);

  const modes: CeremonyModes = useMemo(
    () => ({
      planning: settings?.planning_mode ?? "auto",
      review: settings?.review_mode ?? "auto",
      retro: settings?.retro_mode ?? "auto",
    }),
    [settings],
  );

  const projectSprints = useMemo(
    () => (sprints ?? []).filter((s) => !s.project_id || s.project_id === projectId),
    [sprints, projectId],
  );

  const model = useMemo(
    () =>
      buildFlow({
        phases: designRun?.phases ?? [],
        sprints: projectSprints,
        tickets: tickets ?? [],
        modes,
        stateOf: phaseState,
      }),
    [designRun, projectSprints, tickets, modes],
  );

  const laid = useMemo(() => layoutFlow(model), [model]);

  // Hidratar selección + fullscreen + viewport desde la URL (una vez).
  useEffect(() => {
    if (hydrated.current) return;
    hydrated.current = true;
    const p = new URLSearchParams(window.location.search);
    const node = p.get("node");
    if (node) setSel(node);
    if (p.get("fs") === "1") setFullscreen(true);
    const vp = p.get("vp");
    if (vp) {
      const [x, y, z] = vp.split(",").map(Number);
      if ([x, y, z].every((n) => Number.isFinite(n))) savedVp.current = { x, y, zoom: z };
    }
  }, []);

  // (Re)aplica el layout cuando cambia el modelo, preservando la selección para el
  // resaltado. Restaura el viewport de la URL si existe; si no, ajusta a la vista.
  useEffect(() => {
    setNodes(laid.nodes.map((n) => ({ ...n, selected: n.id === sel })));
    setEdges(laid.edges);
    requestAnimationFrame(() => {
      if (savedVp.current) {
        setViewport(savedVp.current);
        savedVp.current = null;
      } else {
        fitView({ padding: 0.16, duration: 350 });
      }
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [laid]);

  // Mantener el flag `selected` de cada nodo en sync con `sel` sin re-layout.
  useEffect(() => {
    setNodes((ns) => ns.map((n) => (n.selected === (n.id === sel) ? n : { ...n, selected: n.id === sel })));
  }, [sel, setNodes]);

  // Reflejar selección + fullscreen en la URL (replaceState = sin spam de history).
  useEffect(() => {
    if (!hydrated.current) return;
    const p = new URLSearchParams(window.location.search);
    if (sel) p.set("node", sel);
    else p.delete("node");
    if (fullscreen) p.set("fs", "1");
    else p.delete("fs");
    const qs = p.toString();
    window.history.replaceState(null, "", qs ? `?${qs}` : window.location.pathname);
  }, [sel, fullscreen]);

  // Refit al entrar/salir de fullscreen (doble pase: el canvas cambia de tamaño).
  useEffect(() => {
    const raf = requestAnimationFrame(() => fitView({ padding: 0.16, duration: 200 }));
    const tid = setTimeout(() => fitView({ padding: 0.16, duration: 300 }), 280);
    return () => {
      cancelAnimationFrame(raf);
      clearTimeout(tid);
    };
  }, [fullscreen, fitView]);

  // Escape cierra fullscreen o deselecciona.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (fullscreen) setFullscreen(false);
      else setSel(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [fullscreen]);

  const persistVp = useCallback((_: unknown, vp: Viewport) => {
    if (!hydrated.current) return;
    const p = new URLSearchParams(window.location.search);
    p.set("vp", `${Math.round(vp.x)},${Math.round(vp.y)},${vp.zoom.toFixed(2)}`);
    window.history.replaceState(null, "", `?${p.toString()}`);
  }, []);

  const onNodeClick = useCallback(
    (_: unknown, node: Node) => {
      const kind = (node.data as { kind?: string }).kind;
      if (kind === "sprintHeader") {
        const sid = (node.data as { sprintId: string }).sprintId;
        router.push(`/tickets?view=sprints&sprint=${encodeURIComponent(sid)}`);
        return;
      }
      if (kind === "band" || kind === "sprintGroup") return;
      setSel(node.id);
    },
    [router],
  );

  const relayout = useCallback(() => {
    setNodes(laid.nodes.map((n) => ({ ...n, selected: n.id === sel })));
    setEdges(laid.edges);
    requestAnimationFrame(() => fitView({ padding: 0.16, duration: 350 }));
  }, [laid, sel, setNodes, setEdges, fitView]);

  // ── Selección → drawers (derivados del id de nodo + modelo) ──────────────────
  const selPhase = useMemo(() => {
    if (!designRun || !sel) return null;
    const m = sel.match(/^(?:phase|gate):(.+)$/);
    if (!m) return null;
    const phase = designRun.phases.find((p) => p.stepId === m[1]);
    return phase ? { runId: designRun.id, phase } : null;
  }, [sel, designRun]);

  const openTicketId = sel?.startsWith("story:") ? sel.slice("story:".length) : null;
  const openTicket = openTicketId ? (tickets ?? []).find((tk) => tk.id === openTicketId) ?? null : null;

  const selCeremony = useMemo(() => {
    if (!sel?.startsWith("ceremony:")) return null;
    return model.ceremonies.find((c) => c.id === sel) ?? null;
  }, [sel, model]);

  const hasAnything = model.hasDesign || model.hasExecution;

  return (
    <div className={`dag-wrap${fullscreen ? " full" : ""}`}>
      <div className="dag-toolbar">
        <button className="dag-btn" onClick={relayout} title={t("flow.toolbar.relayout")}>
          {t("flow.toolbar.relayout")}
        </button>
        <button
          className="dag-btn"
          onClick={() => fitView({ padding: 0.16, duration: 350 })}
          title={t("flow.toolbar.fitTitle")}
        >
          {t("flow.toolbar.fit")}
        </button>
        <button className="dag-btn primary" onClick={() => setFullscreen((v) => !v)}>
          {fullscreen ? t("flow.toolbar.exitFullscreen") : t("flow.toolbar.fullscreen")}
        </button>
      </div>

      {/* Leyenda de estados de story — misma fuente (statusToken) que el resto. */}
      <div className="dag-legend" aria-hidden>
        {STATUS_ORDER.map((s) => {
          const tok = statusToken(s);
          return (
            <span key={s} className="lg">
              <i style={{ background: s === "backlog" ? "#fff" : tok.soft, borderColor: tok.border }} />
              {t(`tickets.statusLabel.${s}`)}
            </span>
          );
        })}
      </div>

      <div className="dag-canvas">
        {!projectId ? (
          <div className="flow-empty">
            <p>{t("flow.noProject")}</p>
          </div>
        ) : !hasAnything ? (
          <div className="flow-empty">
            <h3>{t("flow.empty.title")}</h3>
            <p>{t("flow.empty.body")}</p>
            <button className="btn primary" onClick={() => router.push("/studio")}>
              {t("flow.empty.cta")}
            </button>
          </div>
        ) : (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={onNodeClick}
            onMoveEnd={persistVp}
            nodeTypes={NODE_TYPES}
            fitView
            fitViewOptions={{ padding: 0.16 }}
            minZoom={0.15}
            maxZoom={2}
            proOptions={{ hideAttribution: true }}
          >
            <Background color="rgba(13,13,15,0.09)" gap={22} size={1} />
            <Controls style={{ boxShadow: "none", border: "1px solid var(--stroke-strong)", borderRadius: 8 }} />
            <MiniMap
              pannable
              zoomable
              style={{ background: "var(--bg2)", border: "1px solid var(--stroke)", borderRadius: 8 }}
              nodeColor={minimapColor}
            />
          </ReactFlow>
        )}
      </div>

      {/* Drawer de fase / gate → PhasePanel (artefacto + aprobar/rechazar/responder). */}
      {selPhase && (
        <>
          <div className="overlay on" onClick={() => setSel(null)} />
          <aside className="drawer on flow-phase-drawer">
            <div className="dh">
              <div>
                <h3>{selPhase.phase.name}</h3>
                <div className="tk">{selPhase.phase.stepId}</div>
              </div>
              <button className="x" onClick={() => setSel(null)}>
                ✕
              </button>
            </div>
            <div className="db">
              <PhasePanel runId={selPhase.runId} phase={selPhase.phase} state={phaseState(selPhase.phase)} />
            </div>
          </aside>
        </>
      )}

      {/* Drawer de ticket (reusa TicketDetail del Board). */}
      <TicketDetail
        ticket={openTicket}
        projectId={projectId}
        onClose={() => setSel(null)}
        onOpenTicket={(id) => setSel(`story:${id}`)}
        onOpenRun={(rid) => setSel(`run:${rid}`)}
      />

      {/* Drawer de run → RunDrawer: una ceremonia (con botón de preview si el review
          fue aceptado, #23) o el run legacy de una story (prefijo run:). */}
      <RunDrawer
        runId={selCeremony?.runId ?? (sel?.startsWith("run:") ? sel.slice("run:".length) : null)}
        onClose={() => setSel(null)}
        footer={
          selCeremony?.kind === "review" && selCeremony.accepted && projectId ? (
            <PreviewButton projectId={projectId} runId={selCeremony.runId} />
          ) : undefined
        }
      />
    </div>
  );
}

// Botón que mintea el token de preview del increment revisado y abre la URL
// devuelta en una tab nueva (#23 — el cableado pendiente del review aceptado).
function PreviewButton({ projectId, runId }: { projectId: string; runId: string }) {
  const t = useT();
  const mint = useMintPreviewToken(projectId);
  const [err, setErr] = useState<string | null>(null);
  async function open() {
    setErr(null);
    try {
      const res = await mint.mutateAsync(runId);
      window.open(res.url, "_blank", "noopener");
    } catch (e) {
      setErr(e instanceof Error && e.message.includes("404") ? t("flow.preview.unavailable") : t("flow.preview.error"));
    }
  }
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 14 }}>
      <button className="btn primary" onClick={open} disabled={mint.isPending}>
        {mint.isPending ? t("flow.preview.opening") : `▶ ${t("flow.preview.open")}`}
      </button>
      {err && <div className="placeholder err">{err}</div>}
    </div>
  );
}

export function FlowView() {
  return (
    <ReactFlowProvider>
      <FlowInner />
    </ReactFlowProvider>
  );
}
