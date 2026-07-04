"use client";

// TICKETS — espejo de JIRA/GitHub por MCP (doc 16 §2.3).
// Cableado contra GET /tickets del STORE NATIVO (control-plane, API_URL): la UI
// es self-contained. Si el control-plane no está, cae al mock.
//
// Layout JIRA-style (rediseño 2026-07): header delgado + toolbar de filtros
// persistente + canvas full-bleed que llena el viewport restante. Las vistas
// (kanban/grafo) SON la página — sin cajas-widget alrededor. Estado view/ticket/
// run viaja en la URL (linkeable/refresh-safe, patrón ?run= del Board).

import { useEffect, useMemo, useState } from "react";
import {
  useCreateStory,
  useDispatch,
  useDispatchCandidates,
  useExecutors,
  useEpics,
  useExportBacklog,
  useTickets,
} from "@/lib/hooks";
import { useActiveProject, useActiveProjectId } from "@/lib/activeProject";
import { ApiError } from "@/lib/api";
import type { ExportResult } from "@/lib/api";
import type { DispatchCandidate, ExecutorInfo } from "@/lib/types";
import { RunDrawer } from "@/components/board/RunDrawer";
import { DepGraph } from "@/components/tickets/DepGraph";
import { KanbanBoard } from "@/components/tickets/KanbanBoard";
import { LaneChip } from "@/components/tickets/LaneChip";
import { SprintsView } from "@/components/tickets/SprintsView";
import { TicketDetail } from "@/components/tickets/TicketDetail";
import { statusToken } from "@/lib/statusToken";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";
import { useT } from "@/lib/i18n";

type ViewKind = "tabla" | "sprints" | "kanban" | "grafo";
const VIEWS: ViewKind[] = ["tabla", "sprints", "kanban", "grafo"];
const DEFAULT_VIEW: ViewKind = "kanban";

// ─────────────────────────────────────────────────────────────────────────────
// Gating por sprint (CLAUDE.md #21): en modo sprint una story "ready" cuyo
// sprint espera a otros sprints NO va a dispararse. Devuelve, por story, la
// lista de sprints que su sprint está esperando (deps cross-sprint no done).
// ─────────────────────────────────────────────────────────────────────────────
function waitingBySprint(tickets: OrchestratorTicket[]): Map<string, string[]> {
  const sprintOf = new Map(tickets.map((t) => [t.id, t.sprint_id || ""]));
  const doneIds = new Set(tickets.filter((t) => t.status === "done").map((t) => t.id));
  const waiting = new Map<string, Set<string>>();
  for (const t of tickets) {
    const sid = t.sprint_id || "";
    if (!sid) continue;
    for (const d of t.deps ?? []) {
      const depSprint = sprintOf.get(d);
      if (depSprint && depSprint !== sid && !doneIds.has(d)) {
        (waiting.get(sid) ?? waiting.set(sid, new Set()).get(sid)!).add(depSprint);
      }
    }
  }
  const out = new Map<string, string[]>();
  for (const [sid, set] of waiting) out.set(sid, [...set].sort());
  return out;
}

// ─────────────────────────────────────────────────────────────────────────────
// Componente raíz
// ─────────────────────────────────────────────────────────────────────────────

export function TicketsView() {
  const t = useT();
  const projectId = useActiveProjectId();
  const { project } = useActiveProject();
  const { data: tickets, isLoading, isError, refetch } = useTickets(projectId);
  const [openRunId, setOpenRunId] = useState<string | null>(null);
  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  const [showNewStory, setShowNewStory] = useState(false);
  const [showExport, setShowExport] = useState(false);
  const [view, setView] = useState<ViewKind>(DEFAULT_VIEW);

  // Filtros (barra persistente, patrón JIRA). Aplican a las 4 vistas.
  const [q, setQ] = useState("");
  const [fStatus, setFStatus] = useState<TicketStatus | "all">("all");
  const [fSprint, setFSprint] = useState<string>("all");
  const [fLane, setFLane] = useState<string>("all");

  // Despacho (F2): el ready-set del conductor. candidateOf mapea story→candidato
  // (directo en modo story; vía su sprint en goal-mode) para pintar el botón ▶.
  const { data: dispatchData } = useDispatchCandidates(projectId);
  const dispatchMut = useDispatch(projectId);
  const candidateOf = useMemo(() => {
    const m = new Map<string, DispatchCandidate>();
    for (const c of dispatchData?.candidates ?? []) {
      if (c.kind === "story") m.set(c.id, c);
      else for (const sid of c.stories ?? []) m.set(sid, c);
    }
    return m;
  }, [dispatchData]);

  // Selector de canal: el despacho abre un picker con los canales REALMENTE
  // disponibles en el GitHub del proyecto (probe del backend), en vez de un
  // confirm ciego con el canal del setting.
  const [pickerFor, setPickerFor] = useState<DispatchCandidate | null>(null);
  const { data: executors } = useExecutors(project?.id ?? null);

  function handleDispatch(c: DispatchCandidate) {
    setPickerFor(c);
  }

  function fireDispatch(c: DispatchCandidate, executor: string) {
    setPickerFor(null);
    dispatchMut.mutate(
      c.kind === "sprint" ? { sprint_id: c.id, executor } : { story_id: c.id, executor },
      {
        onError: (err) => window.alert(t("tickets.dispatch.error") + "\n" + (err instanceof Error ? err.message : String(err))),
      },
    );
  }

  // Hidratar estado desde la URL una vez (deep-link / refresh-safe).
  useEffect(() => {
    const p = new URLSearchParams(window.location.search);
    const v = p.get("view") as ViewKind | null;
    if (v && VIEWS.includes(v)) setView(v);
    const tk = p.get("ticket");
    if (tk) setOpenTicketId(tk);
    const r = p.get("run");
    if (r) setOpenRunId(r);
  }, []);

  // Reflejar estado a la URL (replaceState: sin entradas de history por click).
  useEffect(() => {
    const p = new URLSearchParams(window.location.search);
    if (view === DEFAULT_VIEW) p.delete("view");
    else p.set("view", view);
    if (openTicketId) p.set("ticket", openTicketId);
    else p.delete("ticket");
    if (openRunId) p.set("run", openRunId);
    else p.delete("run");
    const qs = p.toString();
    window.history.replaceState(null, "", qs ? `?${qs}` : window.location.pathname);
  }, [view, openTicketId, openRunId]);

  // Escape cierra el drawer superior (run > ticket), espejo del Board.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (openRunId) setOpenRunId(null);
      else setOpenTicketId(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openRunId]);

  const list = useMemo(() => tickets ?? [], [tickets]);
  const openTicket = openTicketId ? list.find((tk) => tk.id === openTicketId) ?? null : null;

  const sprints = useMemo(
    () => [...new Set(list.map((tk) => tk.sprint_id).filter(Boolean))].sort() as string[],
    [list],
  );
  const lanes = useMemo(
    () => [...new Set(list.map((tk) => tk.owner).filter(Boolean))].sort() as string[],
    [list],
  );
  const gates = useMemo(() => waitingBySprint(list), [list]);

  const filtered = useMemo(() => {
    let out = list;
    if (fStatus !== "all") out = out.filter((tk) => tk.status === fStatus);
    if (fSprint !== "all") out = out.filter((tk) => (tk.sprint_id || "") === fSprint);
    if (fLane !== "all") out = out.filter((tk) => (tk.owner || "") === fLane);
    const needle = q.trim().toLowerCase();
    if (needle) {
      out = out.filter(
        (tk) =>
          tk.id.toLowerCase().includes(needle) ||
          tk.title.toLowerCase().includes(needle) ||
          (tk.body ?? "").toLowerCase().includes(needle),
      );
    }
    return out;
  }, [list, fStatus, fSprint, fLane, q]);

  const hasFilter = q.trim() !== "" || fStatus !== "all" || fSprint !== "all" || fLane !== "all";

  const viewDesc =
    view === "sprints"
      ? t("tickets.desc.sprints")
      : view === "kanban"
        ? t("tickets.desc.kanban")
        : view === "grafo"
          ? t("tickets.desc.grafo")
          : t("tickets.desc.tabla");

  return (
    <div className="tickets-shell">
      {/* Header delgado: título + tabs de vista + acción primaria */}
      <div className="tickets-head">
        <h2>{t("tickets.title")}</h2>
        <span className="c">{viewDesc}</span>
        <span className="sp" />
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
          {VIEWS.map((v) => (
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
        <button
          className="btn ghost sm"
          onClick={() => setShowExport(true)}
          disabled={!project?.repo}
          title={project?.repo ? t("tickets.export.button") : t("tickets.export.noRepo")}
        >
          {t("tickets.export.button")}
        </button>
        <button className="btn ghost sm" onClick={() => setShowNewStory(true)}>
          {t("tickets.newStory")}
        </button>
      </div>

      {/* Toolbar persistente: búsqueda + filtros por faceta (patrón JIRA) */}
      <div className="tickets-toolbar">
        <input
          className="inp"
          style={{ width: 220 }}
          placeholder={t("tickets.toolbar.search")}
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <select
          className="inp"
          style={{ width: "auto" }}
          value={fStatus}
          onChange={(e) => setFStatus(e.target.value as TicketStatus | "all")}
        >
          <option value="all">{t("tickets.toolbar.allStatuses")}</option>
          {(["backlog", "ready", "running", "in_review", "done", "failed"] as TicketStatus[]).map((s) => (
            <option key={s} value={s}>
              {t(`tickets.statusLabel.${s}`)}
            </option>
          ))}
        </select>
        {sprints.length > 0 && (
          <select
            className="inp"
            style={{ width: "auto" }}
            value={fSprint}
            onChange={(e) => setFSprint(e.target.value)}
          >
            <option value="all">{t("tickets.toolbar.allSprints")}</option>
            {sprints.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        )}
        {lanes.length > 0 && (
          <select
            className="inp"
            style={{ width: "auto" }}
            value={fLane}
            onChange={(e) => setFLane(e.target.value)}
          >
            <option value="all">{t("tickets.toolbar.allLanes")}</option>
            {lanes.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
        )}
        {hasFilter && (
          <button
            className="btn ghost sm"
            onClick={() => {
              setQ("");
              setFStatus("all");
              setFSprint("all");
              setFLane("all");
            }}
          >
            {t("tickets.toolbar.clear")}
          </button>
        )}
        <span className="sp" style={{ flex: 1 }} />
        <span className="tickets-count">
          {hasFilter
            ? t("tickets.toolbar.countFiltered", { n: filtered.length, total: list.length })
            : t("tickets.storiesCount", { n: list.length })}
        </span>
      </div>

      {/* Canvas: cada vista llena la región. */}
      {isLoading ? (
        <div className="tickets-canvas pad">
          <div className="placeholder">
            <div className="ph-ic">
              <span className="spin" />
            </div>
            {t("tickets.loading")}
          </div>
        </div>
      ) : isError ? (
        <div className="tickets-canvas pad">
          <div className="placeholder err">
            <div className="ph-ic">⚠</div>
            {t("tickets.error")}{" "}
            <button className="btn ghost sm" onClick={() => refetch()}>
              {t("tickets.retry")}
            </button>
          </div>
        </div>
      ) : list.length === 0 ? (
        <div className="tickets-canvas pad">
          <div className="placeholder">
            <div className="ph-ic">☰</div>
            {t("tickets.empty")}
          </div>
        </div>
      ) : view === "tabla" ? (
        <div className="tickets-canvas pad">
          <TicketsTable
            tickets={filtered}
            gates={gates}
            onOpenTicket={setOpenTicketId}
            onOpenRun={setOpenRunId}
          />
        </div>
      ) : view === "sprints" ? (
        <div className="tickets-canvas pad">
          <SprintsView tickets={filtered} onOpenTicket={setOpenTicketId} />
        </div>
      ) : view === "kanban" ? (
        <div className="tickets-canvas">
          <KanbanBoard
            tickets={filtered}
            gates={gates}
            candidates={candidateOf}
            onDispatch={handleDispatch}
            onOpenTicket={setOpenTicketId}
            onOpenRun={setOpenRunId}
          />
        </div>
      ) : (
        <div className="tickets-canvas">
          <DepGraph tickets={filtered} onOpenTicket={setOpenTicketId} />
        </div>
      )}

      {/* Modal: nueva story */}
      <div className={`overlay ${showNewStory ? "on" : ""}`} onClick={() => setShowNewStory(false)} />
      {showNewStory && (
        <NewStoryModal existingIds={list.map((tk) => tk.id)} projectId={projectId ?? ""} onClose={() => setShowNewStory(false)} />
      )}

      {pickerFor && (
        <>
          <div className="overlay on" onClick={() => setPickerFor(null)} />
          <div className="chan-picker" role="dialog" aria-modal="true">
            <h3>
              {pickerFor.kind === "sprint"
                ? t("tickets.dispatch.pickerSprint", { id: pickerFor.id, n: String(pickerFor.stories?.length ?? 0) })
                : t("tickets.dispatch.pickerStory", { id: pickerFor.id })}
            </h3>
            <p className="c">{t("tickets.dispatch.pickerHint")}</p>
            {(executors ?? []).map((ex: ExecutorInfo) => (
              <button
                key={ex.id}
                className="chan-opt"
                disabled={!ex.available}
                onClick={() => fireDispatch(pickerFor, ex.id)}
                title={ex.available ? "" : ex.reason}
              >
                <span className="chan-name">
                  {ex.id === "copilot" ? t("tickets.dispatch.chanCopilot") : t("tickets.dispatch.chanClaude")}
                  {ex.default ? ` · ${t("tickets.dispatch.chanDefault")}` : ""}
                </span>
                <span className="chan-state">{ex.available ? "🟢" : "⛔"}</span>
                {!ex.available && ex.reason && <span className="chan-why">{ex.reason}</span>}
              </button>
            ))}
            <button className="btn ghost sm" onClick={() => setPickerFor(null)} style={{ marginTop: 10 }}>
              {t("tickets.dispatch.pickerCancel")}
            </button>
          </div>
        </>
      )}

      {/* Modal: exportar backlog a GitHub */}
      <div className={`overlay ${showExport ? "on" : ""}`} onClick={() => setShowExport(false)} />
      {showExport && projectId && (
        <ExportModal
          projectId={projectId}
          repo={project?.repo ?? ""}
          storyCount={list.length}
          onClose={() => setShowExport(false)}
        />
      )}

      {/* Detalle del ticket (la story en sí) — abre para cualquier ticket. Desde
          aquí se baja a la ejecución si la story tiene run. */}
      <TicketDetail
        ticket={openTicket}
        candidate={openTicket ? candidateOf.get(openTicket.id) : undefined}
        onDispatch={handleDispatch}
        onClose={() => setOpenTicketId(null)}
        onOpenTicket={setOpenTicketId}
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
// Vista Tabla — lista densa: lane (assignee), sprint, deps, estado, PR y run.
// ─────────────────────────────────────────────────────────────────────────────

function TicketsTable({
  tickets,
  gates,
  onOpenTicket,
  onOpenRun,
}: {
  tickets: OrchestratorTicket[];
  gates: Map<string, string[]>;
  onOpenTicket: (id: string) => void;
  onOpenRun: (id: string) => void;
}) {
  const t = useT();
  return (
    <div className="ttable">
      <div className="trow">
        <span>{t("tickets.col.id")}</span>
        <span>{t("tickets.col.title")}</span>
        <span>{t("tickets.col.lane")}</span>
        <span>{t("tickets.col.sprint")}</span>
        <span>{t("tickets.col.deps")}</span>
        <span>{t("tickets.col.status")}</span>
        <span>{t("tickets.col.pr")}</span>
        <span>{t("tickets.col.run")}</span>
      </div>
      {tickets.map((tk) => (
        <TicketRow key={tk.id} ticket={tk} gates={gates} onOpenTicket={onOpenTicket} onOpenRun={onOpenRun} />
      ))}
    </div>
  );
}

function TicketRow({
  ticket,
  gates,
  onOpenTicket,
  onOpenRun,
}: {
  ticket: OrchestratorTicket;
  gates: Map<string, string[]>;
  onOpenTicket: (id: string) => void;
  onOpenRun: (id: string) => void;
}) {
  const t = useT();
  const gate = ticket.sprint_id ? gates.get(ticket.sprint_id) : undefined;
  const gated = !!gate?.length && (ticket.status === "ready" || ticket.status === "backlog");
  return (
    <div className="trow click" onClick={() => onOpenTicket(ticket.id)} title={t("tickets.rowTitle")}>
      <span className="id">{ticket.id}</span>
      <span className="ttl">{ticket.title}</span>
      <span>
        {ticket.owner ? <LaneChip lane={ticket.owner} /> : <span style={{ color: "var(--ink4)", fontSize: 12 }}>—</span>}
      </span>
      <span className="mono" style={{ fontSize: 11, color: "var(--ink4)" }}>
        {ticket.sprint_id || "—"}
      </span>
      <span className="dep">{ticket.deps && ticket.deps.length > 0 ? ticket.deps.join(", ") : "—"}</span>
      <span>
        <span
          className={`pill ${statusToken(ticket.status).pill}`}
          title={gated ? t("tickets.sprints.waitingOn", { list: gate!.join(", ") }) : undefined}
          style={gated ? { opacity: 0.55 } : undefined}
        >
          {t(`tickets.status.${ticket.status}`)}
        </span>
      </span>
      <span>
        {ticket.pr_url ? (
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
        ) : (
          <span style={{ color: "var(--ink4)", fontSize: 12 }}>—</span>
        )}
      </span>
      <span>
        {ticket.run_id ? (
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
        ) : (
          <span style={{ color: "var(--ink4)", fontSize: 12 }}>—</span>
        )}
      </span>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Modal: exportar backlog a GitHub
// SÍNCRONO y lento (crea decenas de issues + deps): estado de progreso
// indeterminado claro mientras corre, resumen created/skipped/deps al terminar.
// ─────────────────────────────────────────────────────────────────────────────

function ExportModal({
  projectId,
  repo,
  storyCount,
  onClose,
}: {
  projectId: string;
  repo: string;
  storyCount: number;
  onClose: () => void;
}) {
  const t = useT();
  const exportBacklog = useExportBacklog();
  const [result, setResult] = useState<ExportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const running = exportBacklog.isPending;

  // Cerrar con Escape — bloqueado mientras la exportación corre (evita perder el hilo).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !running) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, running]);

  async function handleExport() {
    setError(null);
    try {
      const res = await exportBacklog.mutateAsync({ projectId, repo: repo || undefined });
      setResult(res);
    } catch (err: unknown) {
      if (err instanceof ApiError) setError(err.message);
      else setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="modal on" role="dialog" aria-modal="true" aria-labelledby="export-title">
      <div className="mh">
        <h3 id="export-title">{t("tickets.export.confirmTitle")}</h3>
        <button
          className="x"
          onClick={onClose}
          disabled={running}
          aria-label={t("tickets.modal.close")}
        >
          ✕
        </button>
      </div>
      <div className="mb">
        {result ? (
          // Resumen final
          <>
            <div className="placeholder" style={{ marginBottom: 12 }}>
              <div className="ph-ic">✓</div>
              {t("tickets.export.done")}
            </div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginBottom: 14 }}>
              <span className="tag">{t("tickets.export.issuesCreated", { n: result.issues_created })}</span>
              <span className="tag">{t("tickets.export.issuesSkipped", { n: result.issues_skipped })}</span>
              <span className="tag">{t("tickets.export.labelsCreated", { n: result.labels_created })}</span>
              <span className="tag">{t("tickets.export.depsCreated", { n: result.deps_created })}</span>
              <span className="tag">{t("tickets.export.depsSkipped", { n: result.deps_skipped })}</span>
            </div>
            <div style={{ display: "flex", gap: 10 }}>
              {result.repo && (
                <a
                  className="btn ghost"
                  style={{ flex: 1, textAlign: "center", textDecoration: "none" }}
                  href={result.repo}
                  target="_blank"
                  rel="noreferrer"
                >
                  {t("tickets.export.openRepo")}
                </a>
              )}
              <button type="button" className="btn primary" style={{ flex: 1 }} onClick={onClose}>
                {t("tickets.export.close")}
              </button>
            </div>
          </>
        ) : (
          // Confirmación / progreso
          <>
            <p style={{ fontSize: 13, color: "var(--ink2)", marginBottom: 8, lineHeight: 1.5 }}>
              {t("tickets.export.confirmBody", { n: storyCount, repo })}
            </p>
            {running && (
              <div className="placeholder" style={{ marginBottom: 12 }}>
                <div className="ph-ic">
                  <span className="spin" />
                </div>
                {t("tickets.export.running", { n: storyCount })}
              </div>
            )}
            {error && (
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
                {t("tickets.export.error")}
                {"\n"}
                {error}
              </div>
            )}
            <div style={{ display: "flex", gap: 10, marginTop: 8 }}>
              <button
                type="button"
                className="btn ghost"
                style={{ flex: 1 }}
                onClick={onClose}
                disabled={running}
              >
                {t("tickets.modal.cancel")}
              </button>
              <button
                type="button"
                className="btn primary"
                style={{ flex: 1 }}
                onClick={handleExport}
                disabled={running}
              >
                {running ? t("tickets.export.running", { n: storyCount }) : t("tickets.export.confirmCta")}
              </button>
            </div>
          </>
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
  projectId,
  onClose,
}: {
  existingIds: string[];
  projectId: string;
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
        project_id: projectId || undefined,
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
