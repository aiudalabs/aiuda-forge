"use client";

// TICKETS — espejo de JIRA/GitHub por MCP (doc 16 §2.3).
// Cableado contra GET /tickets del STORE NATIVO (control-plane, API_URL): la UI
// es self-contained. Si el control-plane no está, cae al mock.
// Los run_id se enlazan al drawer del Board.

import { useEffect, useState } from "react";
import { useCreateStory, useEpics, useTickets } from "@/lib/hooks";
import { useActiveProjectId } from "@/lib/activeProject";
import { ApiError } from "@/lib/api";
import { RunDrawer } from "@/components/board/RunDrawer";
import { DepGraph } from "@/components/tickets/DepGraph";
import { KanbanBoard } from "@/components/tickets/KanbanBoard";
import { TicketDetail } from "@/components/tickets/TicketDetail";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";

type TicketsView = "tabla" | "kanban" | "grafo";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const STATUS_LABEL: Record<TicketStatus, string> = {
  backlog: "backlog",
  ready: "listo",
  running: "running",
  in_review: "en revisión",
  done: "done",
  failed: "FALLIDO",
};

// Clase CSS para la pill de estado — reutiliza los mismos tokens del mockup.
const STATUS_CLASS: Record<TicketStatus, string> = {
  backlog: "queued",
  ready: "run_",
  running: "run_",
  in_review: "queued",
  done: "done",
  failed: "fail",
};

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function TicketsView() {
  const projectId = useActiveProjectId();
  const { data: tickets, isLoading, isError, refetch } = useTickets(projectId);
  const [openRunId, setOpenRunId] = useState<string | null>(null);
  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  const [showNewStory, setShowNewStory] = useState(false);
  const [view, setView] = useState<TicketsView>("tabla");

  const list = tickets ?? [];
  const openTicket = openTicketId ? list.find((t) => t.id === openTicketId) ?? null : null;

  return (
    <div className={`wrap${view !== "tabla" ? " bleed" : ""}`}>
      <div className="sectitle">
        <h2>Tickets</h2>
        <span className="c">store nativo · backlog</span>
        <span className="sp" />
        <span className="tag">{list.length} stories</span>

        {/* Toggle Tabla / Kanban / Grafo */}
        <div
          style={{
            display: "flex",
            gap: 2,
            background: "var(--bg2)",
            border: "1px solid var(--stroke)",
            borderRadius: 10,
            padding: 3,
          }}
        >
          {(["tabla", "kanban", "grafo"] as TicketsView[]).map((v) => (
            <button
              key={v}
              className={`btn sm${view === v ? " primary" : " ghost"}`}
              style={{ borderRadius: 7, textTransform: "capitalize", border: "none", boxShadow: "none" }}
              onClick={() => setView(v)}
            >
              {v.charAt(0).toUpperCase() + v.slice(1)}
            </button>
          ))}
        </div>

        <button className="btn ghost sm" onClick={() => setShowNewStory(true)}>
          + Nueva story
        </button>
      </div>

      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          Cargando tickets…
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          No se pudo conectar al store de tickets.{" "}
          <button className="btn ghost sm" onClick={() => refetch()}>
            Reintentar
          </button>
        </div>
      ) : list.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">☰</div>
          Sin tickets todavía.
        </div>
      ) : view === "tabla" ? (
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
              onOpenTicket={setOpenTicketId}
            />
          ))}
        </div>
      ) : view === "kanban" ? (
        <>
          <div className="sectitle" style={{ marginTop: 4 }}>
            <h2>Kanban</h2>
            <span className="c">agrupado por estado</span>
          </div>
          <KanbanBoard tickets={list} onOpenTicket={setOpenTicketId} />
        </>
      ) : (
        <>
          <div className="sectitle" style={{ marginTop: 4 }}>
            <h2>Grafo DAG</h2>
            <span className="c">dependencias · niveles topológicos</span>
          </div>
          <DepGraph tickets={list} onOpenTicket={setOpenTicketId} />
        </>
      )}

      {/* Modal: nueva story */}
      <div
        className={`overlay ${showNewStory ? "on" : ""}`}
        onClick={() => setShowNewStory(false)}
      />
      {showNewStory && (
        <NewStoryModal
          existingIds={list.map((t) => t.id)}
          onClose={() => setShowNewStory(false)}
        />
      )}

      {/* Detalle del ticket (la story en sí) — abre para cualquier ticket. Desde
          aquí se baja a la ejecución si la story tiene run. */}
      <TicketDetail
        ticket={openTicket}
        onClose={() => setOpenTicketId(null)}
        onOpenRun={(rid) => {
          setOpenTicketId(null);
          setOpenRunId(rid);
        }}
      />

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
  onOpenTicket,
}: {
  ticket: OrchestratorTicket;
  onOpenTicket: (id: string) => void;
}) {
  return (
    <div className="trow click" onClick={() => onOpenTicket(ticket.id)} title="Ver detalle del ticket">
      <span className="id">{ticket.id}</span>
      <span className="ttl">{ticket.title}</span>
      <span className="dep">{ticket.deps && ticket.deps.length > 0 ? ticket.deps.join(", ") : "—"}</span>
      <span>
        <span className={`pill ${STATUS_CLASS[ticket.status]}`}>
          {STATUS_LABEL[ticket.status]}
        </span>
      </span>
      <span>
        {ticket.run_id ? (
          <span className="mono" style={{ fontSize: 11, color: "var(--ink4)" }}>
            {ticket.run_id.slice(0, 12)}…
          </span>
        ) : (
          <span style={{ color: "var(--ink4)", fontSize: 12 }}>—</span>
        )}
      </span>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Modal: nueva story
// ─────────────────────────────────────────────────────────────────────────────

function NewStoryModal({
  existingIds,
  onClose,
}: {
  existingIds: string[];
  onClose: () => void;
}) {
  const { data: epics } = useEpics();
  const createStory = useCreateStory();

  const [id, setId] = useState("");
  const [title, setTitle] = useState("");
  const [selectedDeps, setSelectedDeps] = useState<string[]>([]);
  const [epicId, setEpicId] = useState("");
  const [formError, setFormError] = useState<string | null>(null);

  // Cerrar con Escape.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  function toggleDep(depId: string) {
    setSelectedDeps((prev) =>
      prev.includes(depId) ? prev.filter((d) => d !== depId) : [...prev, depId]
    );
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const trimmedId = id.trim();
    const trimmedTitle = title.trim();

    if (!trimmedId) {
      setFormError("El ID es obligatorio.");
      return;
    }
    if (!trimmedTitle) {
      setFormError("El título es obligatorio.");
      return;
    }

    try {
      await createStory.mutateAsync({
        id: trimmedId,
        title: trimmedTitle,
        deps: selectedDeps,
        epic_id: epicId || undefined,
      });
      onClose();
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        setFormError(err.message);
      } else {
        setFormError(err instanceof Error ? err.message : String(err));
      }
    }
  }

  return (
    <div
      className="modal on"
      role="dialog"
      aria-modal="true"
      aria-labelledby="new-story-title"
    >
      <div className="mh">
        <h3 id="new-story-title">Nueva story</h3>
        <button className="x" onClick={onClose} aria-label="Cerrar">
          ✕
        </button>
      </div>
      <form className="mb" onSubmit={handleSubmit}>
        {/* ID */}
        <div className="field">
          <label htmlFor="ns-id">ID</label>
          <input
            id="ns-id"
            className="inp mono"
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="S2-01"
            autoFocus
            required
          />
        </div>

        {/* Título */}
        <div className="field">
          <label htmlFor="ns-title">Título</label>
          <input
            id="ns-title"
            className="inp"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Descripción breve de la story"
            required
          />
        </div>

        {/* Depende de — chips multi-select con los IDs existentes */}
        {existingIds.length > 0 && (
          <div className="field">
            <label>Depende de</label>
            <div className="chips">
              {existingIds.map((eid) => (
                <button
                  key={eid}
                  type="button"
                  className={`chip${selectedDeps.includes(eid) ? " on" : ""}`}
                  onClick={() => toggleDep(eid)}
                >
                  {eid}
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Epic — selector opcional */}
        {epics && epics.length > 0 && (
          <div className="field">
            <label htmlFor="ns-epic">Epic (opcional)</label>
            <select
              id="ns-epic"
              className="inp"
              value={epicId}
              onChange={(e) => setEpicId(e.target.value)}
            >
              <option value="">— Sin epic —</option>
              {epics.map((ep) => (
                <option key={ep.id} value={ep.id}>
                  {ep.id} · {ep.title}
                </option>
              ))}
            </select>
          </div>
        )}

        {/* Error inline */}
        {formError && (
          <div
            style={{
              padding: "8px 12px",
              background: "var(--err-soft, #fff0f0)",
              border: "1px solid var(--err-line, #f5c5c5)",
              borderRadius: 4,
              fontSize: 12,
              color: "var(--err, #c00)",
              marginBottom: 10,
              whiteSpace: "pre-wrap",
            }}
          >
            {formError}
          </div>
        )}

        <div style={{ display: "flex", gap: 10, marginTop: 8 }}>
          <button
            type="button"
            className="btn ghost"
            style={{ flex: 1 }}
            onClick={onClose}
          >
            Cancelar
          </button>
          <button
            type="submit"
            className="btn primary"
            style={{ flex: 1 }}
            disabled={createStory.isPending}
          >
            {createStory.isPending ? "Creando…" : "Crear story"}
          </button>
        </div>
      </form>
    </div>
  );
}
