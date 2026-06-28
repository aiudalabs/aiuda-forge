"use client";

// KanbanBoard — vista de tickets agrupados por estado en columnas.
// Columnas (de izquierda a derecha): open · blocked · ready · firing · done · failed
// Columnas de solo lectura: el scheduler mueve las stories, no la UI.

import type { OrchestratorTicket, TicketStatus } from "@/lib/types";
import { useT } from "@/lib/i18n";

// ─────────────────────────────────────────────────────────────────────────────
// Configuración de columnas
// ─────────────────────────────────────────────────────────────────────────────

interface ColumnConfig {
  status: TicketStatus;
  pillClass: string;
  headerColor: string;
}

const COLUMNS: ColumnConfig[] = [
  { status: "backlog",   pillClass: "queued", headerColor: "var(--ink4)" },
  { status: "ready",     pillClass: "run_",   headerColor: "var(--accent)" },
  { status: "running",   pillClass: "run_",   headerColor: "var(--navy)" },
  { status: "in_review", pillClass: "queued", headerColor: "#7a5d00" },
  { status: "done",      pillClass: "done",   headerColor: "var(--emerald)" },
  { status: "failed",    pillClass: "fail",   headerColor: "#9a2020" },
];

// ─────────────────────────────────────────────────────────────────────────────
// Tarjeta de story dentro de una columna
// ─────────────────────────────────────────────────────────────────────────────

interface StoryCardProps {
  ticket: OrchestratorTicket;
  onOpenTicket: (id: string) => void;
}

function StoryCard({ ticket, onOpenTicket }: StoryCardProps) {
  const t = useT();
  return (
    <div
      className="card click"
      style={{ padding: "11px 13px", boxShadow: "none" }}
      onClick={() => onOpenTicket(ticket.id)}
      title={t("tickets.rowTitle")}
    >
      <div
        style={{
          fontFamily: "var(--mono)",
          fontSize: 11,
          color: "var(--ink4)",
          marginBottom: 3,
        }}
      >
        {ticket.id}
      </div>
      <div style={{ fontSize: 13, fontWeight: 600, lineHeight: 1.35 }}>
        {ticket.title}
      </div>
      {ticket.body && (
        <div
          title={
            ticket.body +
            (ticket.acceptance ? "\n\nAcceptance:\n" + ticket.acceptance : "")
          }
          style={{
            marginTop: 5,
            fontSize: 11.5,
            color: "var(--ink3)",
            lineHeight: 1.4,
            display: "-webkit-box",
            WebkitLineClamp: 3,
            WebkitBoxOrient: "vertical",
            overflow: "hidden",
          }}
        >
          {ticket.body}
        </div>
      )}
      {ticket.run_id && (
        <div style={{ marginTop: 6 }}>
          <span
            className="tag"
            style={{ fontSize: 10.5, color: "var(--accent)", cursor: "pointer" }}
          >
            {ticket.run_id.slice(0, 12)}…
          </span>
        </div>
      )}
      {ticket.deps && ticket.deps.length > 0 && (
        <div
          className="dep"
          style={{ marginTop: 5, fontSize: 10.5 }}
        >
          dep: {ticket.deps.join(", ")}
        </div>
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Columna de Kanban
// ─────────────────────────────────────────────────────────────────────────────

interface KanbanColumnProps {
  config: ColumnConfig;
  tickets: OrchestratorTicket[];
  onOpenTicket: (id: string) => void;
}

function KanbanColumn({ config, tickets, onOpenTicket }: KanbanColumnProps) {
  const t = useT();
  return (
    <div
      style={{
        flex: "1 1 240px",
        minWidth: 200,
        display: "flex",
        flexDirection: "column",
        gap: 0,
      }}
    >
      {/* Cabecera de columna */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          marginBottom: 10,
        }}
      >
        <span
          className={`pill ${config.pillClass}`}
          style={{ fontSize: 10.5, padding: "3px 9px" }}
        >
          {t(`tickets.col.${config.status}`)}
        </span>
        <span
          style={{
            fontFamily: "var(--mono)",
            fontSize: 11,
            color: config.headerColor,
            fontWeight: 700,
          }}
        >
          {tickets.length}
        </span>
      </div>

      {/* Cards con scroll independiente si la columna es alta */}
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 8,
          maxHeight: "calc(100vh - 290px)",
          overflowY: "auto",
          paddingBottom: 4,
          paddingRight: 2,
        }}
      >
        {tickets.length === 0 ? (
          <div
            style={{
              border: "1px dashed var(--stroke-strong)",
              borderRadius: 10,
              padding: "14px 10px",
              textAlign: "center",
              fontSize: 12,
              color: "var(--ink4)",
            }}
          >
            {t("tickets.column.empty")}
          </div>
        ) : (
          tickets.map((t) => (
            <StoryCard key={t.id} ticket={t} onOpenTicket={onOpenTicket} />
          ))
        )}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

interface KanbanBoardProps {
  tickets: OrchestratorTicket[];
  onOpenTicket: (id: string) => void;
}

export function KanbanBoard({ tickets, onOpenTicket }: KanbanBoardProps) {
  // Agrupar tickets por estado
  const byStatus = new Map<TicketStatus, OrchestratorTicket[]>();
  for (const col of COLUMNS) byStatus.set(col.status, []);
  for (const t of tickets) {
    const col = byStatus.get(t.status);
    if (col) col.push(t);
  }

  return (
    <div
      style={{
        background: "#fff",
        border: "1px solid var(--stroke)",
        borderRadius: "var(--r-lg)",
        padding: 18,
        boxShadow: "var(--shadow-soft)",
        overflowX: "auto",
      }}
    >
      <div style={{ display: "flex", gap: 14 }}>
        {COLUMNS.map((col) => (
          <KanbanColumn
            key={col.status}
            config={col}
            tickets={byStatus.get(col.status) ?? []}
            onOpenTicket={onOpenTicket}
          />
        ))}
      </div>
    </div>
  );
}
