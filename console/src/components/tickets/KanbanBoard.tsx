"use client";

// KanbanBoard — vista de tickets agrupados por estado en columnas.
// Columnas (de izquierda a derecha): open · blocked · ready · firing · done · failed
// Columnas de solo lectura: el scheduler mueve las stories, no la UI.

import type { OrchestratorTicket, TicketStatus } from "@/lib/types";

// ─────────────────────────────────────────────────────────────────────────────
// Configuración de columnas
// ─────────────────────────────────────────────────────────────────────────────

interface ColumnConfig {
  status: TicketStatus;
  label: string;
  pillClass: string;
  headerColor: string;
}

const COLUMNS: ColumnConfig[] = [
  { status: "backlog",   label: "Backlog",     pillClass: "queued", headerColor: "var(--ink4)" },
  { status: "ready",     label: "Listo",       pillClass: "run_",   headerColor: "var(--accent)" },
  { status: "running",   label: "Running",     pillClass: "run_",   headerColor: "var(--navy)" },
  { status: "in_review", label: "En revisión", pillClass: "queued", headerColor: "#7a5d00" },
  { status: "done",      label: "Done",        pillClass: "done",   headerColor: "var(--emerald)" },
  { status: "failed",    label: "Fallido",     pillClass: "fail",   headerColor: "#9a2020" },
];

// ─────────────────────────────────────────────────────────────────────────────
// Tarjeta de story dentro de una columna
// ─────────────────────────────────────────────────────────────────────────────

interface StoryCardProps {
  ticket: OrchestratorTicket;
  onOpenRun: (runId: string) => void;
}

function StoryCard({ ticket, onOpenRun }: StoryCardProps) {
  const clickable = !!ticket.run_id;
  return (
    <div
      className={`card${clickable ? " click" : ""}`}
      style={{ padding: "11px 13px", boxShadow: "none" }}
      onClick={clickable ? () => onOpenRun(ticket.run_id as string) : undefined}
      title={clickable ? `Ver run ${ticket.run_id}` : ticket.title}
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
  onOpenRun: (runId: string) => void;
}

function KanbanColumn({ config, tickets, onOpenRun }: KanbanColumnProps) {
  return (
    <div
      style={{
        flex: "0 0 200px",
        minWidth: 0,
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
          {config.label}
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
          maxHeight: 420,
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
            Vacío
          </div>
        ) : (
          tickets.map((t) => (
            <StoryCard key={t.id} ticket={t} onOpenRun={onOpenRun} />
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
  onOpenRun: (runId: string) => void;
}

export function KanbanBoard({ tickets, onOpenRun }: KanbanBoardProps) {
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
      <div style={{ display: "flex", gap: 14, minWidth: "max-content" }}>
        {COLUMNS.map((col) => (
          <KanbanColumn
            key={col.status}
            config={col}
            tickets={byStatus.get(col.status) ?? []}
            onOpenRun={onOpenRun}
          />
        ))}
      </div>
    </div>
  );
}
