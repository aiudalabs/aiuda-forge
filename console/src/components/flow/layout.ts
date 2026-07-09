// layout — posiciona el FlowModel en un eje left-to-right (diseño → handoff →
// ejecución) y produce nodos/aristas de React Flow. Las bandas y los grupos de
// sprint son backdrops (sin pointer events) computados desde los bboxes ya
// posicionados. Los sublayouts por sprint usan dagre (deps intra-sprint). La
// geometría no se testea en unidad (se verifica en browser); la DERIVACIÓN sí
// (flowGraph.test.ts).

import Dagre from "@dagrejs/dagre";
import type { Node, Edge } from "@xyflow/react";
import type { FlowModel, FlowEdgeDesc, StoryNodeDesc, SprintGroupDesc } from "./flowGraph";
import { DIM } from "./nodes";
import type { TicketStatus } from "@/lib/types";

const CENTER_Y = 0;
const GAP_SM = 26; // entre fase y gate
const GAP_PHASE = 30; // entre fases
const COL_GAP = 96; // entre columnas de sprint (deja sitio a las ceremonias)
const BAND_GAP = 72; // salto diseño→handoff→ejecución
const PAD = 16;

// Estado de la dependencia colorea la arista (misma regla que DepGraph).
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

function storyEdgeToRF(e: FlowEdgeDesc): Edge {
  const base = edgeStroke(e.depStatus);
  return {
    id: e.id,
    source: e.source,
    target: e.target,
    animated: e.animated,
    style: e.crossSprint
      ? { ...base, strokeDasharray: "5 4" } // cross-sprint → punteada
      : base,
    markerEnd: { type: "arrowclosed", color: "#8a8a92" } as Edge["markerEnd"],
  };
}

// Spine faint: conecta la secuencia diseño/handoff y handoff→ejecución para que la
// lectura LR sea inequívoca.
function spine(id: string, source: string, target: string): Edge {
  return {
    id,
    source,
    target,
    style: { stroke: "rgba(13,13,15,0.14)", strokeWidth: 1.5 },
    markerEnd: { type: "arrowclosed", color: "#b9b9c0" } as Edge["markerEnd"],
  };
}

interface Placed {
  nodes: Node[];
  edges: Edge[];
}

// Sublayout dagre de un sprint: posiciones locales (top-left, origen 0,0) + bbox.
function layoutSprintStories(
  stories: StoryNodeDesc[],
  intra: FlowEdgeDesc[],
): { pos: Map<string, { x: number; y: number }>; w: number; h: number } {
  const pos = new Map<string, { x: number; y: number }>();
  if (stories.length === 0) return { pos, w: 0, h: 0 };
  const g = new Dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: "LR", nodesep: 20, ranksep: 54, marginx: 0, marginy: 0 });
  stories.forEach((s) => g.setNode(s.id, { width: DIM.storyW, height: DIM.storyH }));
  const ids = new Set(stories.map((s) => s.id));
  intra.forEach((e) => {
    if (ids.has(e.source) && ids.has(e.target)) g.setEdge(e.source, e.target);
  });
  Dagre.layout(g);
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const s of stories) {
    const n = g.node(s.id);
    const x = n.x - DIM.storyW / 2;
    const y = n.y - DIM.storyH / 2;
    pos.set(s.id, { x, y });
    minX = Math.min(minX, x);
    minY = Math.min(minY, y);
    maxX = Math.max(maxX, x + DIM.storyW);
    maxY = Math.max(maxY, y + DIM.storyH);
  }
  // Normaliza a origen 0,0.
  for (const [id, p] of pos) pos.set(id, { x: p.x - minX, y: p.y - minY });
  return { pos, w: maxX - minX, h: maxY - minY };
}

export function layoutFlow(model: FlowModel): Placed {
  const nodes: Node[] = [];
  const edges: Edge[] = [];
  const gateByStep = new Map(model.gates.map((g) => [g.stepId, g]));

  // ── Franja de fases (diseño + handoff) en una fila horizontal ────────────────
  let cursor = PAD;
  let designMinX = cursor;
  let designMaxX = cursor;
  let handoffMinX = Infinity;
  let handoffMaxX = -Infinity;
  let prevPhaseId: string | null = null;
  let lastHandoffPhaseId: string | null = null;
  let firstHandoffSeen = false;

  const lane = [...model.designPhases, ...model.handoffPhases];
  for (const p of lane) {
    if (p.band === "handoff" && !firstHandoffSeen) {
      firstHandoffSeen = true;
      cursor += BAND_GAP; // salto visual diseño → handoff
    }
    const px = cursor;
    nodes.push({
      id: p.id,
      type: "phase",
      position: { x: px, y: CENTER_Y - DIM.phaseH / 2 },
      data: { kind: "phase", stepId: p.stepId, name: p.name, state: p.state, band: p.band },
      zIndex: 3,
    });
    if (p.band === "handoff") {
      handoffMinX = Math.min(handoffMinX, px);
      handoffMaxX = Math.max(handoffMaxX, px + DIM.phaseW);
      lastHandoffPhaseId = p.id;
    } else {
      designMaxX = Math.max(designMaxX, px + DIM.phaseW);
    }
    if (prevPhaseId) edges.push(spine(`spine:${prevPhaseId}->${p.id}`, prevPhaseId, p.id));
    cursor += DIM.phaseW + GAP_SM;

    const gate = gateByStep.get(p.stepId);
    if (gate) {
      const gx = cursor;
      nodes.push({
        id: gate.id,
        type: "gate",
        position: { x: gx, y: CENTER_Y - DIM.gate / 2 },
        data: { kind: "gate", stepId: gate.stepId, name: gate.name, state: gate.state },
        zIndex: 4,
      });
      // spine pasa POR el gate: fase → gate → siguiente fase.
      edges.push(spine(`spine:${p.id}->${gate.id}`, p.id, gate.id));
      if (p.band === "handoff") handoffMaxX = Math.max(handoffMaxX, gx + DIM.gate);
      else designMaxX = Math.max(designMaxX, gx + DIM.gate);
      cursor += DIM.gate + GAP_PHASE;
      prevPhaseId = gate.id;
    } else {
      cursor += GAP_PHASE - GAP_SM;
      prevPhaseId = p.id;
    }
  }

  // ── Ejecución: columnas de sprint con ceremonias intercaladas ────────────────
  const execStart = cursor + (model.hasExecution ? BAND_GAP : 0);
  let execCursor = execStart;
  let execMinX = execStart;
  let execMaxX = execStart;
  let contentMinY = CENTER_Y - DIM.phaseH / 2;
  let contentMaxY = CENTER_Y + DIM.phaseH / 2;

  const ceremoniesBySprint = new Map<string, typeof model.ceremonies>();
  for (const c of model.ceremonies) {
    const arr = ceremoniesBySprint.get(c.sprintId) ?? [];
    arr.push(c);
    ceremoniesBySprint.set(c.sprintId, arr);
  }

  const sprints = model.sprints;
  let firstExecAnchor: string | null = null;

  sprints.forEach((sp: SprintGroupDesc, i) => {
    // Ceremonias del hueco IZQUIERDO de esta columna: planning de ESTE sprint +
    // review/retro del ANTERIOR (aparecen entre columnas consecutivas).
    const prev = i > 0 ? sprints[i - 1] : null;
    const gapCeremonies = [
      ...(ceremoniesBySprint.get(sp.id) ?? []).filter((c) => c.kind === "planning"),
      ...(prev ? (ceremoniesBySprint.get(prev.id) ?? []).filter((c) => c.kind !== "planning") : []),
    ];
    if (gapCeremonies.length > 0) {
      execCursor += COL_GAP; // reserva el hueco para ceremonias
      const cx = execCursor;
      const totalH = gapCeremonies.length * DIM.ceremonyH + (gapCeremonies.length - 1) * 16;
      let cy = CENTER_Y - totalH / 2;
      for (const c of gapCeremonies) {
        nodes.push({
          id: c.id,
          type: "ceremony",
          position: { x: cx, y: cy },
          data: {
            kind: "ceremony",
            ceremonyKind: c.kind,
            sprintId: c.sprintId,
            runId: c.runId,
            state: c.state,
            accepted: c.accepted,
          },
          zIndex: 3,
        });
        contentMinY = Math.min(contentMinY, cy);
        contentMaxY = Math.max(contentMaxY, cy + DIM.ceremonyH);
        cy += DIM.ceremonyH + 16;
      }
      execCursor += DIM.ceremonyW + COL_GAP;
    } else {
      execCursor += COL_GAP;
    }

    // Sublayout de las stories del sprint.
    const intra = model.storyEdges.filter((e) => !e.crossSprint);
    const { pos, w, h } = layoutSprintStories(sp.stories, intra);
    const colX = execCursor;
    const colTop = CENTER_Y - (DIM.headerH + PAD + h) / 2;
    const storyTop = colTop + DIM.headerH + PAD;

    // Backdrop del grupo (detrás, sin pointer events).
    const groupW = Math.max(w, DIM.storyW) + PAD * 2;
    const groupH = DIM.headerH + PAD + h + PAD;
    nodes.push({
      id: `group:${sp.id}`,
      type: "sprintGroup",
      position: { x: colX - PAD, y: colTop - PAD / 2 },
      data: { kind: "sprintGroup" },
      draggable: false,
      selectable: false,
      style: { width: groupW, height: groupH + PAD, pointerEvents: "none" },
      zIndex: 0,
    });
    // Header clickable.
    nodes.push({
      id: `header:${sp.id}`,
      type: "sprintHeader",
      position: { x: colX, y: colTop },
      data: { kind: "sprintHeader", sprintId: sp.id, name: sp.name, goal: sp.goal, seals: sp.seals },
      style: { width: Math.max(w, DIM.storyW) },
      zIndex: 2,
    });
    if (!firstExecAnchor) firstExecAnchor = `header:${sp.id}`;

    for (const s of sp.stories) {
      const p = pos.get(s.id)!;
      const y = storyTop + p.y;
      nodes.push({
        id: s.id,
        type: "story",
        position: { x: colX + p.x, y },
        data: {
          kind: "story",
          ticketId: s.ticketId,
          title: s.title,
          status: s.status,
          owner: s.owner,
          storyKind: s.kind,
          screenKey: s.screenKey,
        },
        zIndex: 3,
      });
      contentMinY = Math.min(contentMinY, y);
      contentMaxY = Math.max(contentMaxY, y + DIM.storyH);
    }
    contentMinY = Math.min(contentMinY, colTop - PAD);
    contentMaxY = Math.max(contentMaxY, colTop + groupH);

    execMaxX = Math.max(execMaxX, colX + Math.max(w, DIM.storyW) + PAD);
    execCursor = colX + Math.max(w, DIM.storyW) + PAD;
  });

  // Spine handoff → primer ancla de ejecución.
  if (lastHandoffPhaseId && firstExecAnchor) {
    edges.push(spine(`spine:handoff->exec`, lastHandoffPhaseId, firstExecAnchor));
  }

  // Aristas de dependencia entre stories.
  for (const e of model.storyEdges) edges.push(storyEdgeToRF(e));

  // ── Backdrops de banda (detrás de todo) ──────────────────────────────────────
  const bandTop = contentMinY - 40;
  const bandH = contentMaxY - contentMinY + 72;
  const bands: { band: "design" | "handoff" | "execution"; x0: number; x1: number }[] = [];
  if (designMaxX > designMinX) bands.push({ band: "design", x0: designMinX - PAD, x1: designMaxX + PAD });
  if (handoffMaxX > handoffMinX) bands.push({ band: "handoff", x0: handoffMinX - PAD, x1: handoffMaxX + PAD });
  if (model.hasExecution && execMaxX > execMinX) bands.push({ band: "execution", x0: execMinX - PAD, x1: execMaxX + PAD });

  for (const b of bands) {
    nodes.push({
      id: `band:${b.band}`,
      type: "band",
      position: { x: b.x0, y: bandTop },
      data: { kind: "band", band: b.band },
      draggable: false,
      selectable: false,
      style: { width: b.x1 - b.x0, height: bandH, pointerEvents: "none" },
      zIndex: -1,
    });
  }

  return { nodes, edges };
}
