"use client";

// DepGraph — story dependency graph (React Flow v12) with a dagre auto-layout.
// Nodes are draggable (organize freely); the toolbar re-applies the auto-layout,
// fits the view, flips direction, and toggles full-screen. Clickable nodes (with a
// run_id) open the Board drawer.

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  Handle,
  Position,
  useNodesState,
  useEdgesState,
  useReactFlow,
  type Node,
  type Edge,
  type NodeProps,
} from "@xyflow/react";
import Dagre from "@dagrejs/dagre";
import "@xyflow/react/dist/style.css";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";
import { useT } from "@/lib/i18n";

// ─────────────────────────────────────────────────────────────────────────────
// Node styling by status — única fuente: lib/statusToken (antes había un map
// local divergente de los otros 4 call sites).
// ─────────────────────────────────────────────────────────────────────────────

import { STATUS_ORDER, statusToken } from "@/lib/statusToken";

function nodeStyle(s: TicketStatus) {
  const tok = statusToken(s);
  return {
    background: s === "backlog" ? "#fff" : tok.soft,
    border: `1.5px solid ${tok.border}`,
    color: s === "backlog" ? "var(--ink4)" : tok.color,
  };
}

// Aristas: el estado de la DEPENDENCIA (source) colorea la arista — una dep
// fallida bloquea en rojo, una running está por desbloquear, una done ya no pesa.
function edgeStroke(source: TicketStatus): { stroke: string; strokeWidth: number } {
  switch (source) {
    case "failed":
      return { stroke: "rgba(180, 30, 30, 0.55)", strokeWidth: 2 };
    case "running":
      return { stroke: "rgba(20, 40, 80, 0.45)", strokeWidth: 2 };
    case "done":
      return { stroke: "rgba(13, 13, 15, 0.12)", strokeWidth: 1.5 };
    default:
      return { stroke: "rgba(13, 13, 15, 0.26)", strokeWidth: 1.5 };
  }
}

interface StoryNodeData extends Record<string, unknown> {
  id: string;
  title: string;
  status: TicketStatus;
  sprint?: string;
  runId?: string;
  onOpenTicket: (id: string) => void;
}

function StoryNode({ data }: NodeProps) {
  const t = useT();
  const d = data as StoryNodeData;
  return (
    <div
      style={{
        ...nodeStyle(d.status),
        borderRadius: 11,
        padding: "8px 14px",
        fontSize: 12,
        fontWeight: 600,
        fontFamily: "var(--display)",
        cursor: "pointer",
        width: NODE_W,
        boxShadow: "0 2px 8px rgba(13,13,15,0.06)",
        lineHeight: 1.35,
        userSelect: "none",
      }}
      onClick={() => d.onOpenTicket(d.id)}
      title={`${d.title} — ${t("tickets.rowTitle")}`}
    >
      <Handle type="target" position={Position.Left} style={{ opacity: 0 }} />
      <Handle type="source" position={Position.Right} style={{ opacity: 0 }} />
      <div style={{ display: "flex", justifyContent: "space-between", gap: 6 }}>
        <span style={{ fontFamily: "var(--mono)", fontSize: 11, opacity: 0.7 }}>{d.id}</span>
        {d.sprint && (
          <span style={{ fontFamily: "var(--mono)", fontSize: 9.5, opacity: 0.5 }}>{d.sprint}</span>
        )}
      </div>
      <div
        style={{
          fontSize: 12,
          lineHeight: 1.3,
          color: "inherit",
          marginTop: 2,
          display: "-webkit-box",
          WebkitLineClamp: 2,
          WebkitBoxOrient: "vertical",
          overflow: "hidden",
        }}
      >
        {d.title} {statusToken(d.status).icon}
      </div>
    </div>
  );
}

const NODE_TYPES = { story: StoryNode };
const NODE_W = 178;
const NODE_H = 56;

// ─────────────────────────────────────────────────────────────────────────────
// Elements + dagre layout
// ─────────────────────────────────────────────────────────────────────────────

type LayoutKind = "hier-lr" | "hier-tb" | "organic" | "circular" | "radial";

const LAYOUTS: { kind: LayoutKind; labelKey: string }[] = [
  { kind: "hier-lr", labelKey: "tickets.graph.layout.hierLr" },
  { kind: "hier-tb", labelKey: "tickets.graph.layout.hierTb" },
  { kind: "organic", labelKey: "tickets.graph.layout.organic" },
  { kind: "circular", labelKey: "tickets.graph.layout.circular" },
  { kind: "radial", labelKey: "tickets.graph.layout.radial" },
];

function buildElements(
  tickets: OrchestratorTicket[],
  onOpenTicket: (id: string) => void,
): { nodes: Node[]; edges: Edge[] } {
  const byId = new Map(tickets.map((t) => [t.id, t]));
  const nodes: Node[] = tickets.map((t) => ({
    id: t.id,
    type: "story",
    position: { x: 0, y: 0 },
    data: {
      id: t.id,
      title: t.title,
      status: t.status,
      sprint: (t as { sprint_id?: string }).sprint_id,
      runId: t.run_id,
      onOpenTicket,
    } satisfies StoryNodeData,
  }));

  const edges: Edge[] = [];
  for (const t of tickets) {
    for (const depId of t.deps) {
      const dep = byId.get(depId);
      if (!dep) continue;
      edges.push({
        id: `${depId}->${t.id}`,
        source: depId,
        target: t.id,
        animated: t.status === "running" || dep.status === "running",
        style: edgeStroke(dep.status),
        markerEnd: { type: "arrowclosed", color: "#8a8a92" } as Edge["markerEnd"],
      });
    }
  }
  return { nodes, edges };
}

// layoutDagre — hierarchical (dagre) in the given direction.
function layoutDagre(nodes: Node[], edges: Edge[], dir: "LR" | "TB"): Node[] {
  const g = new Dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: dir, nodesep: 28, ranksep: 90, marginx: 20, marginy: 20 });
  nodes.forEach((n) => g.setNode(n.id, { width: NODE_W, height: NODE_H }));
  edges.forEach((e) => g.setEdge(e.source, e.target));
  Dagre.layout(g);
  return nodes.map((n) => {
    const p = g.node(n.id);
    return {
      ...n,
      position: { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 },
      targetPosition: dir === "LR" ? Position.Left : Position.Top,
      sourcePosition: dir === "LR" ? Position.Right : Position.Bottom,
    };
  });
}

// dependency depth (longest path from a root) — drives the radial rings.
function depthByNode(nodes: Node[], edges: Edge[]): Map<string, number> {
  const preds = new Map<string, string[]>();
  nodes.forEach((n) => preds.set(n.id, []));
  edges.forEach((e) => preds.get(e.target)?.push(e.source));
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const calc = (id: string): number => {
    if (depth.has(id)) return depth.get(id)!;
    if (visiting.has(id)) return 0; // cycle guard
    visiting.add(id);
    const ps = preds.get(id) ?? [];
    const d = ps.length ? Math.max(...ps.map(calc)) + 1 : 0;
    visiting.delete(id);
    depth.set(id, d);
    return d;
  };
  nodes.forEach((n) => calc(n.id));
  return depth;
}

// layoutCircular — nodes spaced evenly on one ring.
function layoutCircular(nodes: Node[]): Node[] {
  const n = Math.max(nodes.length, 1);
  const R = Math.max(240, n * 26);
  return nodes.map((node, i) => {
    const a = (i / n) * 2 * Math.PI - Math.PI / 2;
    return { ...node, position: { x: Math.cos(a) * R, y: Math.sin(a) * R } };
  });
}

// layoutRadial — concentric rings by dependency depth (roots at the centre).
function layoutRadial(nodes: Node[], edges: Edge[]): Node[] {
  const depth = depthByNode(nodes, edges);
  const byDepth = new Map<number, Node[]>();
  nodes.forEach((n) => {
    const d = depth.get(n.id) ?? 0;
    (byDepth.get(d) ?? byDepth.set(d, []).get(d)!).push(n);
  });
  const ring = 200;
  const out: Node[] = [];
  for (const [d, group] of byDepth) {
    const R = d * ring;
    group.forEach((node, i) => {
      const a = (i / group.length) * 2 * Math.PI - Math.PI / 2;
      out.push({ ...node, position: d === 0 ? { x: 0, y: 0 } : { x: Math.cos(a) * R, y: Math.sin(a) * R } });
    });
  }
  return out;
}

// layoutForce — a compact force-directed (organic) simulation. Repulsion between
// every pair + spring attraction along edges, cooled over a fixed iteration count.
// Cheap and instant for the story-graph sizes we deal with.
function layoutForce(nodes: Node[], edges: Edge[]): Node[] {
  const n = nodes.length;
  if (n === 0) return nodes;
  const idx = new Map(nodes.map((nd, i) => [nd.id, i]));
  const R0 = Math.max(240, n * 24);
  const x = nodes.map((_, i) => Math.cos((i / n) * 2 * Math.PI) * R0);
  const y = nodes.map((_, i) => Math.sin((i / n) * 2 * Math.PI) * R0);
  const links = edges
    .map((e) => [idx.get(e.source), idx.get(e.target)] as [number, number])
    .filter(([a, b]) => a != null && b != null);

  const k = 240; // ideal separation
  const ITER = 320;
  for (let it = 0; it < ITER; it++) {
    const cool = 1 - it / ITER;
    const dx = new Array(n).fill(0);
    const dy = new Array(n).fill(0);
    // repulsion
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        let ddx = x[i] - x[j];
        let ddy = y[i] - y[j];
        let dist2 = ddx * ddx + ddy * ddy || 0.01;
        const f = (k * k) / dist2;
        const d = Math.sqrt(dist2);
        ddx /= d;
        ddy /= d;
        dx[i] += ddx * f;
        dy[i] += ddy * f;
        dx[j] -= ddx * f;
        dy[j] -= ddy * f;
      }
    }
    // attraction along edges
    for (const [a, b] of links) {
      const ddx = x[a] - x[b];
      const ddy = y[a] - y[b];
      const d = Math.sqrt(ddx * ddx + ddy * ddy) || 0.01;
      const f = (d * d) / k;
      const ux = (ddx / d) * f;
      const uy = (ddy / d) * f;
      dx[a] -= ux;
      dy[a] -= uy;
      dx[b] += ux;
      dy[b] += uy;
    }
    const maxStep = 40 * cool;
    for (let i = 0; i < n; i++) {
      const d = Math.sqrt(dx[i] * dx[i] + dy[i] * dy[i]) || 0.01;
      x[i] += (dx[i] / d) * Math.min(d, maxStep);
      y[i] += (dy[i] / d) * Math.min(d, maxStep);
    }
  }
  return nodes.map((node, i) => ({ ...node, position: { x: x[i], y: y[i] } }));
}

// applyLayout dispatches to the chosen algorithm.
function applyLayout(kind: LayoutKind, nodes: Node[], edges: Edge[]): Node[] {
  switch (kind) {
    case "hier-tb":
      return layoutDagre(nodes, edges, "TB");
    case "organic":
      return layoutForce(nodes, edges);
    case "circular":
      return layoutCircular(nodes);
    case "radial":
      return layoutRadial(nodes, edges);
    case "hier-lr":
    default:
      return layoutDagre(nodes, edges, "LR");
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Interactive graph
// ─────────────────────────────────────────────────────────────────────────────

interface DepGraphProps {
  tickets: OrchestratorTicket[];
  onOpenTicket: (id: string) => void;
}

function DepGraphInner({ tickets, onOpenTicket }: DepGraphProps) {
  const t = useT();
  const base = useMemo(() => buildElements(tickets, onOpenTicket), [tickets, onOpenTicket]);
  const [layout, setLayout] = useState<LayoutKind>("hier-lr");
  const [fullscreen, setFullscreen] = useState(false);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const { fitView } = useReactFlow();

  // (Re)apply a layout algorithm and fit the view.
  const relayout = useCallback(
    (kind: LayoutKind) => {
      setNodes(applyLayout(kind, base.nodes, base.edges));
      setEdges(base.edges);
      requestAnimationFrame(() => fitView({ padding: 0.18, duration: 400 }));
    },
    [base, setNodes, setEdges, fitView],
  );

  // Initial layout + whenever the ticket set changes (keep the chosen algorithm).
  useEffect(() => {
    relayout(layout);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base]);

  // Refit when entering/leaving full-screen (the viewport size changed). Doble
  // pase: uno inmediato + uno tras el layout del position:fixed — con uno solo
  // el fit corría antes de que el canvas tuviera su tamaño nuevo y los nodos
  // quedaban chiquitos en una esquina.
  useEffect(() => {
    const raf = requestAnimationFrame(() => fitView({ padding: 0.18, duration: 200 }));
    const tid = setTimeout(() => fitView({ padding: 0.18, duration: 300 }), 280);
    return () => {
      cancelAnimationFrame(raf);
      clearTimeout(tid);
    };
  }, [fullscreen, fitView]);

  // Escape closes full-screen.
  useEffect(() => {
    if (!fullscreen) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setFullscreen(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [fullscreen]);

  function pick(kind: LayoutKind) {
    setLayout(kind);
    relayout(kind);
  }

  return (
    <div className={`dag-wrap${fullscreen ? " full" : ""}`}>
      <div className="dag-toolbar">
        <div className="dag-seg" role="group" aria-label={t("tickets.graph.layout")}>
          {LAYOUTS.map((l) => (
            <button
              key={l.kind}
              className={`dag-seg-btn${layout === l.kind ? " on" : ""}`}
              onClick={() => pick(l.kind)}
            >
              {t(l.labelKey)}
            </button>
          ))}
        </div>
        <button className="dag-btn" onClick={() => fitView({ padding: 0.18, duration: 400 })} title={t("tickets.graph.fitTitle")}>
          {t("tickets.graph.fit")}
        </button>
        <button className="dag-btn primary" onClick={() => setFullscreen((v) => !v)}>
          {fullscreen ? t("tickets.graph.exitFullscreen") : t("tickets.graph.fullscreen")}
        </button>
      </div>
      {/* Leyenda de estados — sin ella el lenguaje de color del grafo es indecodificable */}
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
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          nodeTypes={NODE_TYPES}
          fitView
          fitViewOptions={{ padding: 0.18 }}
          minZoom={0.2}
          maxZoom={2}
          proOptions={{ hideAttribution: true }}
        >
          <Background color="rgba(13,13,15,0.10)" gap={22} size={1} />
          <Controls style={{ boxShadow: "none", border: "1px solid var(--stroke-strong)", borderRadius: 8 }} />
          <MiniMap
            pannable
            zoomable
            style={{ background: "var(--bg2)", border: "1px solid var(--stroke)", borderRadius: 8 }}
            nodeColor={(n) => statusToken((n.data as StoryNodeData).status).border}
          />
        </ReactFlow>
      </div>
    </div>
  );
}

export function DepGraph(props: DepGraphProps) {
  return (
    <ReactFlowProvider>
      <DepGraphInner {...props} />
    </ReactFlowProvider>
  );
}
