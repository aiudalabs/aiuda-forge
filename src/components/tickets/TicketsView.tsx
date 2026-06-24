"use client";

// TICKETS — espejo de JIRA/GitHub por MCP (doc 16 §2.3).
// Cableado contra GET /tickets del orquestador (ORCHESTRATOR_URL, default :9090).
// Si el orquestador no está disponible, cae al mock igual que el control-plane.
// Los run_id se enlazan al drawer del Board.

import { useState } from "react";
import { useTickets } from "@/lib/hooks";
import { RunDrawer } from "@/components/board/RunDrawer";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const STATUS_LABEL: Record<TicketStatus, string> = {
  open: "open",
  blocked: "bloqueado",
  ready: "listo",
  firing: "running",
  done: "done",
};

// Clase CSS para la pill de estado — reutiliza los mismos tokens del mockup.
const STATUS_CLASS: Record<TicketStatus, string> = {
  open: "queued",
  blocked: "queued",
  ready: "run_",
  firing: "run_",
  done: "done",
};

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function TicketsView() {
  const { data: tickets, isLoading, isError, refetch } = useTickets();
  const [openRunId, setOpenRunId] = useState<string | null>(null);

  const list = tickets ?? [];

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>Tickets</h2>
        <span className="c">espejo de GitHub · MCP</span>
        <span className="sp" />
        <span className="tag">orquestador · {list.length} tickets</span>
      </div>

      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          Cargando tickets…
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          No se pudo conectar al orquestador.{" "}
          <button className="btn ghost sm" onClick={() => refetch()}>
            Reintentar
          </button>
        </div>
      ) : list.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">☰</div>
          Sin tickets todavía.
        </div>
      ) : (
        <div className="ttable">
          <div className="trow">
            <span>ID</span>
            <span>Título</span>
            <span>Depende de</span>
            <span>Estado</span>
            <span>Run</span>
          </div>
          {list.map((t) => (
            <TicketRow
              key={t.id}
              ticket={t}
              onOpenRun={setOpenRunId}
            />
          ))}
        </div>
      )}

      {/* Grafo de dependencias */}
      {list.length > 0 && (
        <>
          <div className="sectitle">
            <h2>Grafo de dependencias</h2>
            <span className="c">qué está READY vs bloqueado</span>
          </div>
          <DepGraph tickets={list} onOpenRun={setOpenRunId} />
        </>
      )}

      {/* Drawer del Board para ver el run asociado */}
      <RunDrawer runId={openRunId} onClose={() => setOpenRunId(null)} />
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Fila de ticket
// ─────────────────────────────────────────────────────────────────────────────

function TicketRow({
  ticket,
  onOpenRun,
}: {
  ticket: OrchestratorTicket;
  onOpenRun: (runId: string) => void;
}) {
  return (
    <div className="trow">
      <span className="id">{ticket.id}</span>
      <span>{ticket.title}</span>
      <span className="dep">{ticket.deps && ticket.deps.length > 0 ? ticket.deps.join(", ") : "—"}</span>
      <span>
        <span className={`pill ${STATUS_CLASS[ticket.status]}`}>
          {STATUS_LABEL[ticket.status]}
        </span>
      </span>
      <span>
        {ticket.run_id ? (
          <button
            className="btn ghost sm"
            style={{ fontSize: 11 }}
            onClick={() => onOpenRun(ticket.run_id as string)}
          >
            {ticket.run_id.slice(0, 12)}…
          </button>
        ) : (
          <span style={{ color: "var(--ink4)", fontSize: 12 }}>—</span>
        )}
      </span>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Grafo de dependencias (vista simplificada)
// ─────────────────────────────────────────────────────────────────────────────

function DepGraph({
  tickets,
  onOpenRun,
}: {
  tickets: OrchestratorTicket[];
  onOpenRun: (runId: string) => void;
}) {
  // Agrupar: raíces (sin deps) y los que tienen deps.
  const roots = tickets.filter((t) => t.deps.length === 0);
  const withDeps = tickets.filter((t) => t.deps.length > 0);

  // Para cada ticket con deps, dibujamos: dep1 ──▶ ticket.
  // Si ya dibujamos el ticket como raíz o hijo, lo mostramos sin duplicar.

  return (
    <div className="dag">
      {roots.map((root) => {
        const children = withDeps.filter((t) => t.deps.includes(root.id));
        return (
          <div key={root.id}>
            <div className="dagrow">
              <DepNode ticket={root} onOpenRun={onOpenRun} />
              {children.map((child) => (
                <span key={child.id} style={{ display: "contents" }}>
                  <span className="arr">──▶</span>
                  <DepNode ticket={child} onOpenRun={onOpenRun} />
                </span>
              ))}
            </div>
          </div>
        );
      })}
      {/* Tickets cuya(s) dep no aparecen en la lista — mostrar solos. */}
      {withDeps
        .filter((t) => !tickets.some((r) => r.id === t.deps[0]))
        .map((orphan) => (
          <div key={orphan.id} className="dagrow">
            <DepNode ticket={orphan} onOpenRun={onOpenRun} />
          </div>
        ))}
    </div>
  );
}

function DepNode({
  ticket,
  onOpenRun,
}: {
  ticket: OrchestratorTicket;
  onOpenRun: (runId: string) => void;
}) {
  const classMap: Record<TicketStatus, string> = {
    open: "queued",
    blocked: "blocked",
    ready: "ready",
    firing: "ready",
    done: "done",
  };

  const icon: Record<TicketStatus, string> = {
    open: "",
    blocked: " ⏳",
    ready: " ⟳",
    firing: " ⟳",
    done: " ✓",
  };

  return (
    <span
      className={`node ${classMap[ticket.status]}`}
      style={ticket.run_id ? { cursor: "pointer" } : undefined}
      onClick={ticket.run_id ? () => onOpenRun(ticket.run_id as string) : undefined}
      title={ticket.run_id ? `Ver run ${ticket.run_id}` : undefined}
    >
      {ticket.id}{icon[ticket.status]}
    </span>
  );
}
