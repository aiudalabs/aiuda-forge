"use client";

// TICKETS — espejo de JIRA/GitHub por MCP (doc 16 §2.3).
// Cableado contra GET /tickets del STORE NATIVO (control-plane, API_URL): la UI
// es self-contained. Si el control-plane no está, cae al mock.
// Los run_id se enlazan al drawer del Board.

import { useEffect, useRef, useState } from "react";
import { useCreateStory, useEpics, useTickets } from "@/lib/hooks";
import { ApiError } from "@/lib/api";
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
  failed: "FALLIDO",
};

// Clase CSS para la pill de estado — reutiliza los mismos tokens del mockup.
const STATUS_CLASS: Record<TicketStatus, string> = {
  open: "queued",
  blocked: "queued",
  ready: "run_",
  firing: "run_",
  done: "done",
  failed: "fail",
};

// Clase CSS para el nodo del DAG.
const NODE_CLASS: Record<TicketStatus, string> = {
  open: "",
  blocked: "blocked",
  ready: "ready",
  firing: "ready",
  done: "done",
  failed: "blocked",
};

// Ícono que acompaña al nodo del DAG.
const NODE_ICON: Record<TicketStatus, string> = {
  open: "",
  blocked: " ⏳",
  ready: " ⟳",
  firing: " ⟳",
  done: " ✓",
  failed: " ✗",
};

// ─────────────────────────────────────────────────────────────────────────────
// Cálculo de niveles topológicos para el DAG
// ─────────────────────────────────────────────────────────────────────────────

interface DagNode {
  ticket: OrchestratorTicket;
  level: number;
  isCycle: boolean;
}

/**
 * Asigna un nivel topológico a cada ticket:
 *   nivel 0 → sin deps (o deps ausentes del listado)
 *   nivel N → max(nivel de sus deps) + 1
 *
 * Detecta ciclos: los tickets no resueltos tras MAX_ITER iteraciones se
 * marcan isCycle=true para que no bloqueen el render.
 */
function computeDagLevels(tickets: OrchestratorTicket[]): DagNode[] {
  const MAX_ITER = tickets.length + 2;
  const byId = new Map(tickets.map((t) => [t.id, t]));
  const levels = new Map<string, number>();

  // Iteramos hasta que todos los niveles se estabilicen o agotemos MAX_ITER.
  let changed = true;
  let iter = 0;
  while (changed && iter < MAX_ITER) {
    changed = false;
    iter++;
    for (const t of tickets) {
      const knownDeps = t.deps.filter((d) => byId.has(d));
      let level = 0;
      if (knownDeps.length > 0) {
        const depLevels = knownDeps.map((d) => levels.get(d) ?? -1);
        if (depLevels.some((l) => l < 0)) continue; // dep todavía sin nivel
        level = Math.max(...depLevels) + 1;
      }
      if (!levels.has(t.id) || levels.get(t.id) !== level) {
        levels.set(t.id, level);
        changed = true;
      }
    }
  }

  return tickets.map((t) => ({
    ticket: t,
    level: levels.get(t.id) ?? 0,
    isCycle: !levels.has(t.id),
  }));
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function TicketsView() {
  const { data: tickets, isLoading, isError, refetch } = useTickets();
  const [openRunId, setOpenRunId] = useState<string | null>(null);
  const [showNewStory, setShowNewStory] = useState(false);

  const list = tickets ?? [];

  return (
    <div className="wrap">
      <div className="sectitle">
        <h2>Tickets</h2>
        <span className="c">store nativo · backlog</span>
        <span className="sp" />
        <span className="tag">{list.length} stories</span>
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
            <h2>Grafo DAG</h2>
            <span className="c">dependencias · niveles topológicos</span>
          </div>
          <DepGraph tickets={list} onOpenRun={setOpenRunId} />
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
// Grafo de dependencias — layout por niveles topológicos con aristas SVG
// ─────────────────────────────────────────────────────────────────────────────

function DepGraph({
  tickets,
  onOpenRun,
}: {
  tickets: OrchestratorTicket[];
  onOpenRun: (runId: string) => void;
}) {
  const nodes = computeDagLevels(tickets);
  const cycles = nodes.filter((n) => n.isCycle);

  // Agrupar por nivel.
  const maxLevel = nodes.reduce((mx, n) => Math.max(mx, n.level), 0);
  const cols: DagNode[][] = Array.from({ length: maxLevel + 1 }, (_, i) =>
    nodes.filter((n) => !n.isCycle && n.level === i)
  );

  // Referencias a los nodos del DOM para dibujar las aristas SVG.
  const containerRef = useRef<HTMLDivElement>(null);
  const nodeRefs = useRef<Map<string, HTMLSpanElement>>(new Map());

  // Dimensiones del contenedor para el SVG.
  const [svgSize, setSvgSize] = useState({ w: 0, h: 0 });
  // Lista de aristas: { from, to } en coordenadas relativas al contenedor.
  const [edges, setEdges] = useState<{ x1: number; y1: number; x2: number; y2: number }[]>([]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    function measure() {
      if (!container) return;
      const cr = container.getBoundingClientRect();
      setSvgSize({ w: cr.width, h: cr.height });

      const newEdges: typeof edges = [];
      for (const node of nodes) {
        if (node.isCycle) continue;
        const toEl = nodeRefs.current.get(node.ticket.id);
        if (!toEl) continue;
        const toR = toEl.getBoundingClientRect();
        const toX = toR.left - cr.left + toR.width / 2;
        const toY = toR.top - cr.top + toR.height / 2;

        for (const depId of node.ticket.deps) {
          const fromEl = nodeRefs.current.get(depId);
          if (!fromEl) continue;
          const fromR = fromEl.getBoundingClientRect();
          newEdges.push({
            x1: fromR.left - cr.left + fromR.width / 2,
            y1: fromR.top - cr.top + fromR.height / 2,
            x2: toX,
            y2: toY,
          });
        }
      }
      setEdges(newEdges);
    }

    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(container);
    return () => ro.disconnect();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tickets]);

  return (
    <div className="dag" ref={containerRef} style={{ position: "relative", minHeight: 80 }}>
      {/* SVG de aristas — se dibuja por encima del layout de nodos */}
      {svgSize.w > 0 && (
        <svg
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            width: svgSize.w,
            height: svgSize.h,
            pointerEvents: "none",
            zIndex: 0,
          }}
        >
          {edges.map((e, i) => (
            <line
              key={i}
              x1={e.x1}
              y1={e.y1}
              x2={e.x2}
              y2={e.y2}
              stroke="var(--stroke-strong)"
              strokeWidth={1.5}
              markerEnd="url(#arr)"
            />
          ))}
          <defs>
            <marker id="arr" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
              <path d="M0,0 L0,6 L8,3 z" fill="var(--ink4)" />
            </marker>
          </defs>
        </svg>
      )}

      {/* Columnas de niveles */}
      <div
        style={{
          display: "flex",
          gap: 32,
          alignItems: "flex-start",
          position: "relative",
          zIndex: 1,
        }}
      >
        {cols.map((col, colIdx) => (
          <div
            key={colIdx}
            style={{ display: "flex", flexDirection: "column", gap: 10 }}
          >
            <div
              style={{
                fontSize: 10,
                fontWeight: 700,
                letterSpacing: "0.1em",
                textTransform: "uppercase",
                color: "var(--ink4)",
                marginBottom: 4,
              }}
            >
              L{colIdx}
            </div>
            {col.map(({ ticket }) => (
              <span
                key={ticket.id}
                ref={(el) => {
                  if (el) nodeRefs.current.set(ticket.id, el);
                  else nodeRefs.current.delete(ticket.id);
                }}
                className={`node ${NODE_CLASS[ticket.status]}`}
                style={ticket.run_id ? { cursor: "pointer" } : undefined}
                onClick={ticket.run_id ? () => onOpenRun(ticket.run_id as string) : undefined}
                title={ticket.run_id ? `Ver run ${ticket.run_id}` : ticket.title}
              >
                {ticket.id}{NODE_ICON[ticket.status]}
              </span>
            ))}
          </div>
        ))}

        {/* Nodos en ciclo: se muestran aparte con aviso */}
        {cycles.length > 0 && (
          <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
            <div
              style={{
                fontSize: 10,
                fontWeight: 700,
                letterSpacing: "0.1em",
                textTransform: "uppercase",
                color: "var(--accent)",
                marginBottom: 4,
              }}
            >
              ciclo ⚠
            </div>
            {cycles.map(({ ticket }) => (
              <span
                key={ticket.id}
                ref={(el) => {
                  if (el) nodeRefs.current.set(ticket.id, el);
                  else nodeRefs.current.delete(ticket.id);
                }}
                className="node"
                style={{ borderColor: "var(--accent)", color: "var(--accent)" }}
                title={`Ciclo detectado: ${ticket.id}`}
              >
                {ticket.id} ↺
              </span>
            ))}
          </div>
        )}
      </div>
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
