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

// ─────────────────────────────────────────────────────────────────────────────
// Node styling by status
// ─────────────────────────────────────────────────────────────────────────────

interface StatusStyle {
  background: string;
  border: string;
  color: string;
}

const STATUS_STYLE: Record<TicketStatus, StatusStyle> = {
  backlog:   { background: "#fff",                 border: "1.5px solid rgba(13,13,15,0.14)", color: "#8a8a92" },
  ready:     { background: "rgba(232,68,10,0.07)", border: "1.5px solid rgba(232,68,10,0.24)", color: "#e8440a" },
  running:   { background: "rgba(20,40,80,0.07)",  border: "1.5px solid rgba(20,40,80,0.3)",  color: "#142850" },
  in_review: { background: "rgba(180,140,20,0.08)",border: "1.5px solid rgba(180,140,20,0.4)", color: "#7a5d00" },
  done:      { background: "rgba(10,123,90,0.07)", border: "1.5px solid #0a7b5a",             color: "#0a7b5a" },
  failed:    { background: "rgba(180,30,30,0.08)", border: "1.5px solid #c55",                color: "#9a2020" },
};

const STATUS_ICON: Record<TicketStatus, string> = {
  backlog: " ⏳",
  ready: " ⟳",
  running: " ⟳",
  in_review: " ⌾",
  done: " ✓",
  failed: " ✗",
};

interface StoryNodeData extends Record<string, unknown> {
  id: string;
  title: string;
  status: TicketStatus;
  sprint?: string;
  runId?: string;
  onOpenRun: (runId: string) => void;
}

function StoryNode({ data }: NodeProps) {
  const d = data as StoryNodeData;
  const style = STATUS_STYLE[d.status] ?? STATUS_STYLE.backlog;
  return (
    <div
      style={{
        ...style,
        borderRadius: 11,
        padding: "8px 14px",
        fontSize: 12,
        fontWeight: 600,
        fontFamily: "var(--display)",
        cursor: d.runId ? "pointer" : "default",
        width: NODE_W,
        boxShadow: "0 2px 8px rgba(13,13,15,0.06)",
        lineHeight: 1.35,
        userSelect: "none",
      }}
      onClick={d.runId ? () => d.onOpenRun(d.runId as string) : undefined}
      title={d.runId ? `Ver run ${d.runId}` : d.title}
    >
      <Handle type="target" position={Position.Left} style={{ opacity: 0 }} />
      <Handle type="source" position={Position.Right} style={{ opacity: 0 }} />
      <div style={{ display: "flex", justifyContent: "space-between", gap: 6 }}>
        <span style={{ fontFamily: "var(--mono)", fontSize: 11, opacity: 0.7 }}>{d.id}</span>
        {d.sprint && (
          <span style={{ fontFamily: "var(--mono)", fontSize: 9.5, opacity: 0.5 }}>{d.sprint}</span>
        )}
      </div>
      <div style={{ fontSize: 12, lineHeight: 1.3, color: "inherit", marginTop: 2 }}>
        {d.title.length > 38 ? d.title.slice(0, 37) + "…" : d.title}
        {STATUS_ICON[d.status]}
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

type Dir = "LR" | "TB";

function buildElements(
  tickets: OrchestratorTicket[],
  onOpenRun: (runId: string) => void,
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
      onOpenRun,
    } satisfies StoryNodeData,
  }));

  const edges: Edge[] = [];
  for (const t of tickets) {
    for (const depId of t.deps) {
      if (!byId.has(depId)) continue;
      edges.push({
        id: `${depId}->${t.id}`,
        source: depId,
        target: t.id,
        animated: t.status === "running",
        style: { stroke: "rgba(13,13,15,0.18)", strokeWidth: 1.5 },
        markerEnd: { type: "arrowclosed", color: "#8a8a92" } as Edge["markerEnd"],
      });
    }
  }
  return { nodes, edges };
}

// layoutDagre returns the nodes positioned by a dagre hierarchical layout.
function layoutDagre(nodes: Node[], edges: Edge[], dir: Dir): Node[] {
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

// ─────────────────────────────────────────────────────────────────────────────
// Interactive graph
// ─────────────────────────────────────────────────────────────────────────────

interface DepGraphProps {
  tickets: OrchestratorTicket[];
  onOpenRun: (runId: string) => void;
}

function DepGraphInner({ tickets, onOpenRun }: DepGraphProps) {
  const base = useMemo(() => buildElements(tickets, onOpenRun), [tickets, onOpenRun]);
  const [dir, setDir] = useState<Dir>("LR");
  const [fullscreen, setFullscreen] = useState(false);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const { fitView } = useReactFlow();

  // (Re)apply the dagre auto-layout — also the "Auto-layout" button action.
  const relayout = useCallback(
    (d: Dir = dir) => {
      setNodes(layoutDagre(base.nodes, base.edges, d));
      setEdges(base.edges);
      requestAnimationFrame(() => fitView({ padding: 0.18, duration: 400 }));
    },
    [base, dir, setNodes, setEdges, fitView],
  );

  // Initial layout + whenever the ticket set changes.
  useEffect(() => {
    relayout(dir);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base]);

  // Refit when entering/leaving full-screen (the viewport size changed).
  useEffect(() => {
    const id = requestAnimationFrame(() => fitView({ padding: 0.18, duration: 300 }));
    return () => cancelAnimationFrame(id);
  }, [fullscreen, fitView]);

  // Escape closes full-screen.
  useEffect(() => {
    if (!fullscreen) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setFullscreen(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [fullscreen]);

  function flipDir() {
    const next: Dir = dir === "LR" ? "TB" : "LR";
    setDir(next);
    relayout(next);
  }

  return (
    <div className={`dag-wrap${fullscreen ? " full" : ""}`}>
      <div className="dag-toolbar">
        <button className="dag-btn" onClick={() => relayout()} title="Reorganizar (auto-layout)">
          ⤢ Auto-layout
        </button>
        <button className="dag-btn" onClick={() => fitView({ padding: 0.18, duration: 400 })} title="Ajustar a pantalla">
          ⊡ Ajustar
        </button>
        <button className="dag-btn" onClick={flipDir} title="Cambiar dirección">
          {dir === "LR" ? "↳ Horizontal" : "↴ Vertical"}
        </button>
        <button className="dag-btn primary" onClick={() => setFullscreen((v) => !v)}>
          {fullscreen ? "✕ Salir" : "⛶ Pantalla completa"}
        </button>
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
            nodeColor={(n) => {
              const s = STATUS_STYLE[(n.data as StoryNodeData).status] ?? STATUS_STYLE.backlog;
              return s.border.replace("1.5px solid ", "");
            }}
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
