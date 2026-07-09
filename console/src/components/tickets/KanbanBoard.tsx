"use client";

// KanbanBoard — vista de tickets agrupados por estado en columnas (JIRA-like).
// Columnas de solo lectura: el scheduler mueve las stories, no la UI.
//
// Rediseño 2026-07: el board ES la página (sin caja wrapper con borde/sombra);
// columnas separadas por hairlines que llenan la altura del canvas, cada una con
// scroll propio. Columnas colapsables a un riel vertical (las vacías colapsan por
// defecto) para que las 6 quepan sin scroll horizontal. Cards con la meta que
// JIRA/Linear muestran de un vistazo: lane (assignee), ACs, sprint, PR y run.

import { useState } from "react";
import type { DispatchCandidate, OrchestratorTicket, TicketStatus } from "@/lib/types";
import { LaneChip } from "@/components/tickets/LaneChip";
import { STATUS_ORDER, statusToken } from "@/lib/statusToken";
import { isAgentLost } from "@/lib/agentLost";
import { useT } from "@/lib/i18n";

function acceptanceCount(accept?: string): number {
  if (!accept) return 0;
  return accept
    .split(/\r?\n/)
    .map((l) => l.replace(/^\s*[-*•]\s*/, "").trim())
    .filter(Boolean).length;
}

// ─────────────────────────────────────────────────────────────────────────────
// Tarjeta de story dentro de una columna
// ─────────────────────────────────────────────────────────────────────────────

interface StoryCardProps {
  ticket: OrchestratorTicket;
  gate?: string[];
  candidate?: DispatchCandidate;
  onDispatch?: (c: DispatchCandidate) => void;
  onOpenTicket: (id: string) => void;
  onOpenRun: (id: string) => void;
}

function StoryCard({ ticket, gate, candidate, onDispatch, onOpenTicket, onOpenRun }: StoryCardProps) {
  const t = useT();
  const acs = acceptanceCount(ticket.acceptance);
  const gated = !!gate?.length && (ticket.status === "ready" || ticket.status === "backlog");
  return (
    <div
      className={`kb-card${gated ? " gated" : ""}`}
      onClick={() => onOpenTicket(ticket.id)}
      title={t("tickets.rowTitle")}
    >
      <div className="kb-top">
        <span className="kb-id">{ticket.id}</span>
        {ticket.sprint_id && <span className="kb-sprint">{ticket.sprint_id}</span>}
      </div>
      <div className="kb-ttl">{ticket.title}</div>
      {isAgentLost(ticket) && (
        <div className="kb-lost" title={ticket.agent_lost}>
          ⚠ {t("tickets.card.agentLost")}
        </div>
      )}
      {ticket.body && (
        <div className="kb-body" title={ticket.body}>
          {ticket.body}
        </div>
      )}
      <div className="kb-meta">
        {candidate && onDispatch && (
          <button
            className="kb-dispatch"
            onClick={(e) => {
              e.stopPropagation();
              onDispatch(candidate);
            }}
            title={t("tickets.dispatch.buttonTitle", {
              executor: candidate.executor,
              model: candidate.model || "auto",
            })}
          >
            ▶ {candidate.kind === "sprint" ? candidate.id : t("tickets.dispatch.button")}
          </button>
        )}
        {ticket.owner && <LaneChip lane={ticket.owner} />}
        {acs > 0 && (
          <span className="kb-acs" title={t("tickets.card.acsTitle", { n: acs })}>
            ✓ {acs}
          </span>
        )}
        {ticket.pr_url && (
          <a
            className="kb-pr"
            href={ticket.pr_url}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            title={t("tickets.card.openPR")}
          >
            PR ↗
          </a>
        )}
        {ticket.session_url && ticket.status === "running" && (
          <a
            className="kb-pr"
            href={ticket.session_url}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            title={t("tickets.card.openSession")}
          >
            {t("tickets.card.session")} ↗
          </a>
        )}
        {ticket.run_id && !ticket.session_url && !ticket.external_ref && (
          <button
            className="kb-run"
            onClick={(e) => {
              e.stopPropagation();
              onOpenRun(ticket.run_id!);
            }}
            title={t("tickets.card.openRun")}
          >
            {ticket.run_id.slice(0, 10)}…
          </button>
        )}
      </div>
      {gated && (
        <div className="kb-gate">⧗ {t("tickets.sprints.waitingOn", { list: gate!.join(", ") })}</div>
      )}
      {ticket.deps && ticket.deps.length > 0 && (
        <div className="kb-deps">dep: {ticket.deps.join(", ")}</div>
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Columna de Kanban (colapsable a riel vertical, patrón JIRA)
// ─────────────────────────────────────────────────────────────────────────────

interface KanbanColumnProps {
  status: TicketStatus;
  tickets: OrchestratorTicket[];
  gates: Map<string, string[]>;
  candidates?: Map<string, DispatchCandidate>;
  onDispatch?: (c: DispatchCandidate) => void;
  collapsed: boolean;
  onToggle: () => void;
  onOpenTicket: (id: string) => void;
  onOpenRun: (id: string) => void;
}

function KanbanColumn({ status, tickets, gates, candidates, onDispatch, collapsed, onToggle, onOpenTicket, onOpenRun }: KanbanColumnProps) {
  const t = useT();
  const tok = statusToken(status);

  if (collapsed) {
    return (
      <div
        className="kb-col collapsed"
        onClick={onToggle}
        role="button"
        title={t("tickets.kanban.expand")}
      >
        <span className="kb-count" style={{ color: tok.color }}>
          {tickets.length}
        </span>
        <span className="kb-vlabel">{t(`tickets.col.${status}`)}</span>
      </div>
    );
  }

  return (
    <div className="kb-col">
      <button className="kb-col-head" onClick={onToggle} title={t("tickets.kanban.collapse")}>
        <span className={`pill ${tok.pill}`} style={{ fontSize: 10.5, padding: "3px 9px" }}>
          {t(`tickets.col.${status}`)}
        </span>
        <span className="kb-count" style={{ color: tok.color }}>
          {tickets.length}
        </span>
        <span className="kb-caret">◂</span>
      </button>
      <div className="kb-cards">
        {tickets.length === 0 ? (
          <div className="kb-empty">{t("tickets.column.empty")}</div>
        ) : (
          tickets.map((tk) => (
            <StoryCard
              key={tk.id}
              ticket={tk}
              gate={tk.sprint_id ? gates.get(tk.sprint_id) : undefined}
              candidate={candidates?.get(tk.id)}
              onDispatch={onDispatch}
              onOpenTicket={onOpenTicket}
              onOpenRun={onOpenRun}
            />
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
  gates: Map<string, string[]>;
  candidates?: Map<string, DispatchCandidate>;
  onDispatch?: (c: DispatchCandidate) => void;
  onOpenTicket: (id: string) => void;
  onOpenRun: (id: string) => void;
}

export function KanbanBoard({ tickets, gates, candidates, onDispatch, onOpenTicket, onOpenRun }: KanbanBoardProps) {
  // Agrupar tickets por estado
  const byStatus = new Map<TicketStatus, OrchestratorTicket[]>();
  for (const s of STATUS_ORDER) byStatus.set(s, []);
  for (const tk of tickets) {
    const col = byStatus.get(tk.status);
    if (col) col.push(tk);
  }

  // Colapso: manual del usuario > default (vacía = colapsada). El default es
  // derivado, no estado: al llegar datos la columna se auto-expande sola.
  const [userCollapsed, setUserCollapsed] = useState<Partial<Record<TicketStatus, boolean>>>({});
  const isCollapsed = (s: TicketStatus) =>
    userCollapsed[s] ?? (byStatus.get(s)?.length ?? 0) === 0;

  return (
    <div className="kanban">
      {STATUS_ORDER.map((s) => (
        <KanbanColumn
          key={s}
          status={s}
          tickets={byStatus.get(s) ?? []}
          gates={gates}
          candidates={candidates}
          onDispatch={onDispatch}
          collapsed={isCollapsed(s)}
          onToggle={() => setUserCollapsed((c) => ({ ...c, [s]: !isCollapsed(s) }))}
          onOpenTicket={onOpenTicket}
          onOpenRun={onOpenRun}
        />
      ))}
    </div>
  );
}
