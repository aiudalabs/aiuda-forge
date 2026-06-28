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
import { SprintsView } from "@/components/tickets/SprintsView";
import { TicketDetail } from "@/components/tickets/TicketDetail";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";
import { useT } from "@/lib/i18n";

type TicketsView = "tabla" | "sprints" | "kanban" | "grafo";

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

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
  const t = useT();
  const projectId = useActiveProjectId();
  const { data: tickets, isLoading, isError, refetch } = useTickets(projectId);
  const [openRunId, setOpenRunId] = useState<string | null>(null);
  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  const [showNewStory, setShowNewStory] = useState(false);
  const [view, setView] = useState<TicketsView>("tabla");

  const list = tickets ?? [];
  const openTicket = openTicketId ? list.find((t) => t.id === openTicketId) ?? null : null;

  // Single, view-aware descriptor — avoids repeating "store nativo · backlog" on top
  // of a second "Kanban / Grafo DAG" sub-header (they were redundant).
  const viewDesc =
    view === "sprints"
      ? t("tickets.desc.sprints")
      : view === "kanban"
        ? t("tickets.desc.kanban")
        : view === "grafo"
          ? t("tickets.desc.grafo")
          : t("tickets.desc.tabla");

  return (
    <div className={`wrap${view === "kanban" || view === "grafo" ? " bleed" : ""}`}>
      <div className="sectitle">
        <h2>{t("tickets.title")}</h2>
        <span className="c">{viewDesc}</span>
        <span className="sp" />
        <span className="tag">{t("tickets.storiesCount", { n: list.length })}</span>

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
          {(["tabla", "sprints", "kanban", "grafo"] as TicketsView[]).map((v) => (
            <button
              key={v}
              className={`btn sm${view === v ? " primary" : " ghost"}`}
              style={{ borderRadius: 7, border: "none", boxShadow: "none" }}
              onClick={() => setView(v)}
            >
              {t(`tickets.tab.${v}`)}
            </button>
          ))}
        </div>

        <button className="btn ghost sm" onClick={() => setShowNewStory(true)}>
          {t("tickets.newStory")}
        </button>
      </div>

      {isLoading ? (
        <div className="placeholder">
          <div className="ph-ic"><span className="spin" /></div>
          {t("tickets.loading")}
        </div>
      ) : isError ? (
        <div className="placeholder err">
          <div className="ph-ic">⚠</div>
          {t("tickets.error")}{" "}
          <button className="btn ghost sm" onClick={() => refetch()}>
            {t("tickets.retry")}
          </button>
        </div>
      ) : list.length === 0 ? (
        <div className="placeholder">
          <div className="ph-ic">☰</div>
          {t("tickets.empty")}
        </div>
      ) : view === "tabla" ? (
        <div className="ttable">
          <div className="trow">
            <span>{t("tickets.col.id")}</span>
            <span>{t("tickets.col.title")}</span>
            <span>{t("tickets.col.deps")}</span>
            <span>{t("tickets.col.status")}</span>
            <span>{t("tickets.col.run")}</span>
          </div>
          {list.map((t) => (
            <TicketRow
              key={t.id}
              ticket={t}
              onOpenTicket={setOpenTicketId}
            />
          ))}
        </div>
      ) : view === "sprints" ? (
        <SprintsView tickets={list} onOpenTicket={setOpenTicketId} />
      ) : view === "kanban" ? (
        <KanbanBoard tickets={list} onOpenTicket={setOpenTicketId} />
      ) : (
        <DepGraph tickets={list} onOpenTicket={setOpenTicketId} />
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
  const t = useT();
  return (
    <div className="trow click" onClick={() => onOpenTicket(ticket.id)} title={t("tickets.rowTitle")}>
      <span className="id">{ticket.id}</span>
      <span className="ttl">{ticket.title}</span>
      <span className="dep">{ticket.deps && ticket.deps.length > 0 ? ticket.deps.join(", ") : "—"}</span>
      <span>
        <span className={`pill ${STATUS_CLASS[ticket.status]}`}>
          {t(`tickets.status.${ticket.status}`)}
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
  const t = useT();
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
      setFormError(t("tickets.modal.errId"));
      return;
    }
    if (!trimmedTitle) {
      setFormError(t("tickets.modal.errTitle"));
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
        <h3 id="new-story-title">{t("tickets.modal.title")}</h3>
        <button className="x" onClick={onClose} aria-label={t("tickets.modal.close")}>
          ✕
        </button>
      </div>
      <form className="mb" onSubmit={handleSubmit}>
        {/* ID */}
        <div className="field">
          <label htmlFor="ns-id">{t("tickets.modal.idLabel")}</label>
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
          <label htmlFor="ns-title">{t("tickets.modal.titleLabel")}</label>
          <input
            id="ns-title"
            className="inp"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t("tickets.modal.titlePlaceholder")}
            required
          />
        </div>

        {/* Depende de — chips multi-select con los IDs existentes */}
        {existingIds.length > 0 && (
          <div className="field">
            <label>{t("tickets.modal.depsLabel")}</label>
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
            <label htmlFor="ns-epic">{t("tickets.modal.epicLabel")}</label>
            <select
              id="ns-epic"
              className="inp"
              value={epicId}
              onChange={(e) => setEpicId(e.target.value)}
            >
              <option value="">{t("tickets.modal.epicNone")}</option>
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
            {t("tickets.modal.cancel")}
          </button>
          <button
            type="submit"
            className="btn primary"
            style={{ flex: 1 }}
            disabled={createStory.isPending}
          >
            {createStory.isPending ? t("tickets.modal.creating") : t("tickets.modal.create")}
          </button>
        </div>
      </form>
    </div>
  );
}
