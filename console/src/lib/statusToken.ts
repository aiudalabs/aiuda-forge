// statusToken — ÚNICA fuente de verdad del lenguaje visual de los 6 estados de una
// story. Antes había 5 maps divergentes (TicketsView, KanbanBoard, DepGraph,
// TicketDetail, SprintsView) que colapsaban ready=running y backlog=in_review al
// mismo color de pill. Todo call site importa de aquí.

import type { TicketStatus } from "./types";

export interface StatusToken {
  /** Modificador de .pill (globals.css): cada estado tiene el suyo. */
  pill: string;
  /** Color fuerte (headers de columna, conteos, leyenda). */
  color: string;
  /** Fondo suave (nodos del grafo, chips). */
  soft: string;
  /** Borde del nodo en el grafo. */
  border: string;
  /** Glifo compacto (grafo / leyenda). */
  icon: string;
}

export const STATUS_ORDER: TicketStatus[] = [
  "backlog",
  "ready",
  "running",
  "in_review",
  "done",
  "failed",
];

export const STATUS_TOKENS: Record<TicketStatus, StatusToken> = {
  backlog: {
    pill: "queued",
    color: "var(--ink4)",
    soft: "var(--bg3)",
    border: "rgba(13, 13, 15, 0.14)",
    icon: "⏳",
  },
  ready: {
    pill: "ready",
    color: "var(--accent)",
    soft: "var(--accent-soft)",
    border: "var(--accent-line)",
    icon: "⟳",
  },
  running: {
    pill: "run_",
    color: "var(--navy)",
    soft: "var(--navy-soft)",
    border: "rgba(20, 40, 80, 0.3)",
    icon: "⟳",
  },
  in_review: {
    pill: "review",
    color: "var(--amber)",
    soft: "var(--amber-soft)",
    border: "rgba(180, 140, 20, 0.4)",
    icon: "⌾",
  },
  done: {
    pill: "done",
    color: "var(--emerald)",
    soft: "var(--emerald-soft)",
    border: "var(--emerald)",
    icon: "✓",
  },
  failed: {
    pill: "fail",
    color: "var(--danger)",
    soft: "var(--danger-soft)",
    border: "#c55",
    icon: "✗",
  },
};

export function statusToken(s: TicketStatus): StatusToken {
  return STATUS_TOKENS[s] ?? STATUS_TOKENS.backlog;
}
