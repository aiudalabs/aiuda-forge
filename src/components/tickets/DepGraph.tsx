"use client";

// DepGraph — grafo de dependencias entre stories con React Flow (v12).
// Calcula un layout topológico por niveles: dep → story que depende.
// Nodos clickables si tienen run_id (abre el drawer del Board).

import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  Handle,
  Position,
  type Node,
  type Edge,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";

// ─────────────────────────────────────────────────────────────────────────────
// Estilos de nodo por estado — reutiliza los tokens CSS del mockup
// ─────────────────────────────────────────────────────────────────────────────

interface StatusStyle {
  background: string;
  border: string;
  color: string;
}

const STATUS_STYLE: Record<TicketStatus, StatusStyle> = {
  open:    { background: "#fff",                          border: "1.5px solid rgba(13,13,15,0.14)", color: "#52525a" },
  blocked: { background: "#fff",                          border: "1.5px solid rgba(13,13,15,0.14)", color: "#8a8a92" },
  ready:   { background: "rgba(232,68,10,0.07)",          border: "1.5px solid rgba(232,68,10,0.24)", color: "#e8440a" },
  firing:  { background: "rgba(20,40,80,0.07)",           border: "1.5px solid rgba(20,40,80,0.3)",  color: "#142850" },
  done:    { background: "rgba(10,123,90,0.07)",          border: "1.5px solid #0a7b5a",             color: "#0a7b5a" },
  failed:  { background: "rgba(180,30,30,0.08)",          border: "1.5px solid #c55",                color: "#9a2020" },
};

const STATUS_ICON: Record<TicketStatus, string> = {
  open:    "",
  blocked: " ⏳",
  ready:   " ⟳",
  firing:  " ⟳",
  done:    " ✓",
  failed:  " ✗",
};

// ─────────────────────────────────────────────────────────────────────────────
// Nodo personalizado
// ─────────────────────────────────────────────────────────────────────────────

interface StoryNodeData extends Record<string, unknown> {
  id: string;
  title: string;
  status: TicketStatus;
  runId?: string;
  onOpenRun: (runId: string) => void;
}

function StoryNode({ data }: NodeProps) {
  const d = data as StoryNodeData;
  const style = STATUS_STYLE[d.status] ?? STATUS_STYLE.open;
  return (
    <div
      style={{
        ...style,
        borderRadius: 10,
        padding: "8px 14px",
        fontSize: 12,
        fontWeight: 600,
        fontFamily: "var(--display)",
        cursor: d.runId ? "pointer" : "default",
        minWidth: 90,
        maxWidth: 180,
        lineHeight: 1.35,
        userSelect: "none",
      }}
      onClick={d.runId ? () => d.onOpenRun(d.runId as string) : undefined}
      title={d.runId ? `Ver run ${d.runId}` : d.title}
    >
      {/* Connection points: without these, React Flow can't draw edges (error #008). */}
      <Handle type="target" position={Position.Left} style={{ opacity: 0 }} />
      <Handle type="source" position={Position.Right} style={{ opacity: 0 }} />
      <div style={{ fontFamily: "var(--mono)", fontSize: 11, opacity: 0.7, marginBottom: 2 }}>
        {d.id}
      </div>
      <div style={{ fontSize: 12, lineHeight: 1.3, color: "inherit" }}>
        {d.title.length > 36 ? d.title.slice(0, 35) + "…" : d.title}
        {STATUS_ICON[d.status]}
      </div>
    </div>
  );
}

const NODE_TYPES = { story: StoryNode };

// ─────────────────────────────────────────────────────────────────────────────
// Cálculo de niveles topológicos (idéntico al de TicketsView pero para coords)
// ─────────────────────────────────────────────────────────────────────────────

interface DagLevel {
  id: string;
  level: number;
  isCycle: boolean;
}

function computeLevels(tickets: OrchestratorTicket[]): DagLevel[] {
  const MAX_ITER = tickets.length + 2;
  const byId = new Map(tickets.map((t) => [t.id, t]));
  const levels = new Map<string, number>();

  let changed = true;
  let iter = 0;
  while (changed && iter < MAX_ITER) {
    changed = false;
    iter++;
    for (const t of tickets) {
      const knownDeps = t.deps.filter((d) => byId.has(d));
      let level = 0;
      if (knownDeps.length > 0) {
        const depLevels = knownDeps.map((d) => levels.get(d) ?? -1);
        if (depLevels.some((l) => l < 0)) continue;
        level = Math.max(...depLevels) + 1;
      }
      if (!levels.has(t.id) || levels.get(t.id) !== level) {
        levels.set(t.id, level);
        changed = true;
      }
    }
  }

  return tickets.map((t) => ({
    id: t.id,
    level: levels.get(t.id) ?? 0,
    isCycle: !levels.has(t.id),
  }));
}

// ─────────────────────────────────────────────────────────────────────────────
// Construcción de nodes + edges para React Flow
// ─────────────────────────────────────────────────────────────────────────────

const LEVEL_GAP_X = 220;
const NODE_GAP_Y = 90;

function buildGraph(
  tickets: OrchestratorTicket[],
  onOpenRun: (runId: string) => void
): { nodes: Node[]; edges: Edge[] } {
  const dagLevels = computeLevels(tickets);
  const levelMap = new Map(dagLevels.map((d) => [d.id, d.level]));

  // Contar cuántos nodos por nivel para calcular offset Y centrado.
  const countByLevel = new Map<number, number>();
  for (const { level } of dagLevels) {
    countByLevel.set(level, (countByLevel.get(level) ?? 0) + 1);
  }

  // Asignar posición a cada nodo: índice dentro del nivel para el eje Y.
  const indexByLevel = new Map<number, number>();
  const nodes: Node[] = [];

  for (const t of tickets) {
    const levelInfo = dagLevels.find((d) => d.id === t.id)!;
    const level = levelInfo.isCycle ? 0 : levelInfo.level;
    const idx = indexByLevel.get(level) ?? 0;
    indexByLevel.set(level, idx + 1);

    const colCount = countByLevel.get(level) ?? 1;
    const totalHeight = (colCount - 1) * NODE_GAP_Y;

    nodes.push({
      id: t.id,
      type: "story",
      position: {
        x: level * LEVEL_GAP_X,
        y: idx * NODE_GAP_Y - totalHeight / 2,
      },
      data: {
        id: t.id,
        title: t.title,
        status: t.status,
        runId: t.run_id,
        onOpenRun,
      } satisfies StoryNodeData,
    });
  }

  // Edges: dep → story (directed left→right)
  const edges: Edge[] = [];
  const ticketById = new Map(tickets.map((t) => [t.id, t]));

  for (const t of tickets) {
    for (const depId of t.deps) {
      if (!ticketById.has(depId)) continue;
      edges.push({
        id: `${depId}->${t.id}`,
        source: depId,
        target: t.id,
        animated: t.status === "firing",
        style: { stroke: "var(--stroke-strong)", strokeWidth: 1.5 },
        markerEnd: { type: "arrowclosed", color: "var(--ink4)" },
      });
    }
  }

  return { nodes, edges };
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente exportado
// ─────────────────────────────────────────────────────────────────────────────

interface DepGraphProps {
  tickets: OrchestratorTicket[];
  onOpenRun: (runId: string) => void;
}

function DepGraphInner({ tickets, onOpenRun }: DepGraphProps) {
  const { nodes, edges } = buildGraph(tickets, onOpenRun);

  return (
    <div
      className="dag"
      style={{ height: 420, padding: 0, overflow: "hidden" }}
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.3}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <Background color="var(--stroke-strong)" gap={20} size={1} />
        <Controls
          style={{ boxShadow: "none", border: "1px solid var(--stroke-strong)", borderRadius: 8 }}
        />
        <MiniMap
          style={{
            background: "var(--bg2)",
            border: "1px solid var(--stroke)",
            borderRadius: 8,
          }}
          nodeColor={(n) => {
            const status = (n.data as StoryNodeData).status;
            const s = STATUS_STYLE[status] ?? STATUS_STYLE.open;
            return s.border.replace("1.5px solid ", "");
          }}
        />
      </ReactFlow>
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
