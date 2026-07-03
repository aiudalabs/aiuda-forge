"use client";

// LaneChip — el "assignee" de una story: el agente/lane responsable (owner).
// Color determinista por nombre de lane, avatar con la inicial. Es la cue de
// escaneo nº1 en JIRA/Linear; antes el owner no se mostraba en ningún lado.

const LANE_COLORS: [string, string][] = [
  ["var(--navy)", "var(--navy-soft)"],
  ["var(--emerald)", "var(--emerald-soft)"],
  ["var(--amber)", "var(--amber-soft)"],
  ["#6d28d9", "rgba(109, 40, 217, 0.09)"],
  ["#0e7490", "rgba(14, 116, 144, 0.09)"],
  ["var(--accent)", "var(--accent-soft)"],
];

function laneColor(lane: string): [string, string] {
  let h = 0;
  for (let i = 0; i < lane.length; i++) h = (h * 31 + lane.charCodeAt(i)) >>> 0;
  return LANE_COLORS[h % LANE_COLORS.length];
}

export function LaneChip({ lane, title }: { lane: string; title?: string }) {
  const [color, soft] = laneColor(lane);
  return (
    <span className="lane-chip" style={{ color, background: soft }} title={title ?? lane}>
      <span className="lane-avatar" style={{ background: color }}>
        {lane.slice(0, 1).toUpperCase()}
      </span>
      {lane}
    </span>
  );
}
