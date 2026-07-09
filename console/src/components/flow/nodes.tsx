"use client";

// Nodos custom del grafo Flow (React Flow v12). Puro display: el click se maneja
// arriba (onNodeClick en FlowView) leyendo node.data.kind — los nodos no llevan
// callbacks. Colores de story vía statusToken (única fuente); estado de fase/gate
// vía PHASE_ICON/PHASE_GLYPH_CLS de phaseHelpers (se reusa, no se redefine).

import { Handle, Position, type NodeProps } from "@xyflow/react";
import { statusToken } from "@/lib/statusToken";
import { PHASE_ICON, PHASE_GLYPH_CLS, type PhaseState } from "@/components/studio/phaseHelpers";
import { LaneChip } from "@/components/tickets/LaneChip";
import { useT } from "@/lib/i18n";
import type { TicketStatus } from "@/lib/types";
import type { CeremonyKind, SealState, SprintSeals } from "./flowGraph";

// Dimensiones compartidas con el layout.
export const DIM = {
  phaseW: 156,
  phaseH: 54,
  gate: 38,
  storyW: 188,
  storyH: 70,
  ceremonyW: 132,
  ceremonyH: 54,
  headerH: 48,
};

const hidden = { opacity: 0 } as const;

// ── Fase del diseño ─────────────────────────────────────────────────────────────
export interface PhaseNodeData extends Record<string, unknown> {
  kind: "phase";
  stepId: string;
  name: string;
  state: PhaseState;
  band: "design" | "handoff";
}

export function PhaseNode({ data }: NodeProps) {
  const d = data as PhaseNodeData;
  return (
    <div className={`flow-node phase st-${d.state} ${d.state === "running" ? "running" : ""}`}>
      <Handle type="target" position={Position.Left} style={hidden} />
      <Handle type="source" position={Position.Right} style={hidden} />
      <div className="flow-phase-name">{d.name}</div>
      <span className={`flow-glyph ${PHASE_GLYPH_CLS[d.state]}`}>{PHASE_ICON[d.state]}</span>
    </div>
  );
}

// ── Gate humano (rombo) ─────────────────────────────────────────────────────────
export interface GateNodeData extends Record<string, unknown> {
  kind: "gate";
  stepId: string;
  name: string;
  state: PhaseState; // answered = "running" (NO approved) — fix #22 reusado
}

export function GateNode({ data }: NodeProps) {
  const t = useT();
  const d = data as GateNodeData;
  const title = d.state === "awaiting" ? t("flow.gate.awaiting") : t("flow.gate.state", { state: d.state });
  return (
    <div
      className={`flow-gate st-${d.state} ${d.state === "running" ? "running" : ""} ${d.state === "awaiting" ? "awaiting" : ""}`}
      title={title}
    >
      <Handle type="target" position={Position.Left} style={hidden} />
      <Handle type="source" position={Position.Right} style={hidden} />
      <span className={`flow-glyph ${PHASE_GLYPH_CLS[d.state]}`}>{PHASE_ICON[d.state]}</span>
    </div>
  );
}

// ── Story ───────────────────────────────────────────────────────────────────────
export interface StoryNodeData extends Record<string, unknown> {
  kind: "story";
  ticketId: string;
  title: string;
  status: TicketStatus;
  owner?: string;
  storyKind: "story" | "bug";
  screenKey?: string;
}

function storyNodeStyle(s: TicketStatus) {
  const tok = statusToken(s);
  return {
    background: s === "backlog" ? "#fff" : tok.soft,
    borderColor: tok.border,
    color: s === "backlog" ? "var(--ink4)" : tok.color,
  };
}

export function StoryNode({ data }: NodeProps) {
  const d = data as StoryNodeData;
  const tok = statusToken(d.status);
  return (
    <div
      className={`flow-node story ${d.status === "running" ? "running" : ""}`}
      style={storyNodeStyle(d.status)}
    >
      <Handle type="target" position={Position.Left} style={hidden} />
      <Handle type="source" position={Position.Right} style={hidden} />
      <div className="flow-story-top">
        <span className="flow-story-id">{d.ticketId}</span>
        <span className="flow-story-glyph">{tok.icon}</span>
      </div>
      <div className="flow-story-title">{d.title}</div>
      <div className="flow-story-meta">
        {d.owner && <LaneChip lane={d.owner} />}
        {d.storyKind === "bug" && <span className="flow-kind bug">bug</span>}
        {d.screenKey && <span className="flow-screen" title={d.screenKey}>▤</span>}
      </div>
    </div>
  );
}

// ── Ceremonia (planning / review / retro) ────────────────────────────────────────
export interface CeremonyNodeData extends Record<string, unknown> {
  kind: "ceremony";
  ceremonyKind: CeremonyKind;
  sprintId: string;
  runId: string;
  state: SealState;
  accepted: boolean;
}

const CEREMONY_GLYPH: Record<CeremonyKind, string> = {
  planning: "◷",
  review: "◎",
  retro: "↻",
};

export function CeremonyNode({ data }: NodeProps) {
  const t = useT();
  const d = data as CeremonyNodeData;
  return (
    <div
      className={`flow-ceremony cer-${d.ceremonyKind} seal-${d.state} ${d.state === "running" ? "running" : ""}`}
      title={`${t(`flow.ceremony.${d.ceremonyKind}`)} · ${d.sprintId} · ${t(`flow.seal.${d.state}`)}`}
    >
      <Handle type="target" position={Position.Left} style={hidden} />
      <Handle type="source" position={Position.Right} style={hidden} />
      <span className="flow-cer-glyph">{CEREMONY_GLYPH[d.ceremonyKind]}</span>
      <div className="flow-cer-body">
        <span className="flow-cer-kind">{t(`flow.ceremony.${d.ceremonyKind}`)}</span>
        <span className="flow-cer-sprint">{d.sprintId}</span>
      </div>
      {d.accepted && <span className="flow-cer-preview" title={t("flow.preview.open")}>▶</span>}
    </div>
  );
}

// ── Header de sprint (clickable) + backdrop del grupo ─────────────────────────────
export interface SprintHeaderData extends Record<string, unknown> {
  kind: "sprintHeader";
  sprintId: string;
  name: string;
  goal: string;
  seals: SprintSeals;
}

const SEAL_LABEL: Record<"planning" | "review" | "retro", string> = {
  planning: "P",
  review: "R",
  retro: "T",
};

export function SprintHeaderNode({ data }: NodeProps) {
  const t = useT();
  const d = data as SprintHeaderData;
  const entries = (["planning", "review", "retro"] as const).filter((k) => d.seals[k] !== undefined);
  return (
    <div className="flow-sprint-head" title={d.goal || d.name}>
      <Handle type="target" position={Position.Left} style={hidden} />
      <span className="flow-sprint-name">{d.name}</span>
      {entries.length > 0 && (
        <span className="flow-seals">
          {entries.map((k) => (
            <span
              key={k}
              className={`flow-seal seal-${d.seals[k]} ${d.seals[k] === "running" ? "running" : ""}`}
              title={`${t(`flow.ceremony.${k}`)}: ${t(`flow.seal.${d.seals[k]}`)}`}
            >
              {SEAL_LABEL[k]}
            </span>
          ))}
        </span>
      )}
    </div>
  );
}

// Backdrop del grupo de sprint — sin pointer events (se fija en el layout).
export function SprintGroupNode() {
  return <div className="flow-sprint-group" />;
}

// Backdrop de banda con etiqueta — sin pointer events.
export interface BandNodeData extends Record<string, unknown> {
  kind: "band";
  band: "design" | "handoff" | "execution";
}

export function BandNode({ data }: NodeProps) {
  const t = useT();
  const d = data as BandNodeData;
  return (
    <div className={`flow-band band-${d.band}`}>
      <span className="flow-band-label">{t(`flow.band.${d.band}`)}</span>
    </div>
  );
}

export const NODE_TYPES = {
  phase: PhaseNode,
  gate: GateNode,
  story: StoryNode,
  ceremony: CeremonyNode,
  sprintHeader: SprintHeaderNode,
  sprintGroup: SprintGroupNode,
  band: BandNode,
};
