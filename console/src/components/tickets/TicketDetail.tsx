"use client";

// TicketDetail (JIRA-like) — the STORY itself, not its execution. Opens for ANY
// ticket (clickable regardless of run state): id, sprint, lane, epic, status, the
// user-story description, falsifiable acceptance criteria, dependencies (clickable
// → navigate the graph), the PR when one exists, and the recovery action (requeue)
// when the story failed — the user shouldn't need to dig 3 levels to unblock work.
// If the story has a run, a button drops one level deeper into the execution.

import { useEffect, useState } from "react";
import { useDeleteStory, useExportStory, useRequeue, useRequeueStory } from "@/lib/hooks";
import { ApiError } from "@/lib/api";
import type { DispatchCandidate, OrchestratorTicket } from "@/lib/types";
import { LaneChip } from "@/components/tickets/LaneChip";
import { statusToken } from "@/lib/statusToken";
import { useT } from "@/lib/i18n";

// Acceptance criteria come as newline / "- " separated lines; render them as a list.
function acceptanceLines(accept?: string): string[] {
  if (!accept) return [];
  return accept
    .split(/\r?\n/)
    .map((l) => l.replace(/^\s*[-*•]\s*/, "").trim())
    .filter(Boolean);
}

// "github:owner/repo#N" → https://github.com/owner/repo/issues/N (or null).
function issueUrl(ref?: string | null): string | null {
  if (!ref) return null;
  const m = ref.match(/^github:(.+)#(\d+)$/);
  return m ? `https://github.com/${m[1]}/issues/${m[2]}` : null;
}

export function TicketDetail({
  ticket,
  projectId,
  candidate,
  onDispatch,
  onClose,
  onOpenTicket,
  onOpenRun,
}: {
  ticket: OrchestratorTicket | null;
  /** Proyecto activo — scopea el export/delete a la story del proyecto correcto. */
  projectId?: string | null;
  candidate?: DispatchCandidate;
  onDispatch?: (c: DispatchCandidate) => void;
  onClose: () => void;
  onOpenTicket: (id: string) => void;
  onOpenRun: (runId: string) => void;
}) {
  const t = useT();
  const requeue = useRequeue();
  const requeueStory = useRequeueStory();
  const exportStory = useExportStory();
  const deleteStory = useDeleteStory();
  const [actionError, setActionError] = useState<string | null>(null);
  const [exportedRef, setExportedRef] = useState<string | null>(null);
  const open = !!ticket;
  const acs = acceptanceLines(ticket?.acceptance);

  // Reset per-ticket feedback al cambiar de story (el drawer se reutiliza).
  useEffect(() => {
    setActionError(null);
    setExportedRef(null);
  }, [ticket?.id]);

  function doExport() {
    if (!ticket) return;
    setActionError(null);
    exportStory.mutate(
      { id: ticket.id, project: projectId ?? undefined },
      {
        onSuccess: (r) => setExportedRef(r.external_ref ?? null),
        onError: (e) => setActionError(e instanceof Error ? e.message : String(e)),
      },
    );
  }

  function doRequeueStory() {
    if (!ticket) return;
    if (!window.confirm(t("tickets.detail.requeueConfirm"))) return;
    setActionError(null);
    requeueStory.mutate(ticket.id, {
      onSuccess: (r) => {
        // requeued:false = ya estaba en backlog (idempotente): no cerramos, avisamos.
        if (r.requeued === false) setActionError(r.reason ?? t("tickets.detail.requeueNoop"));
        else onClose();
      },
      onError: (e) => {
        if (e instanceof ApiError && e.detail) setActionError(e.detail);
        else setActionError(e instanceof Error ? e.message : String(e));
      },
    });
  }

  function doDelete() {
    if (!ticket) return;
    if (!window.confirm(t("tickets.detail.deleteConfirm"))) return;
    setActionError(null);
    deleteStory.mutate(
      { id: ticket.id, project: projectId ?? undefined },
      {
        onSuccess: (r) => {
          if (r.external_ref) window.alert(t("tickets.detail.githubRemains"));
          onClose();
        },
        onError: (e) => {
          if (e instanceof ApiError && e.status === 409) setActionError(t("tickets.detail.deleteBlocked"));
          else setActionError(e instanceof Error ? e.message : String(e));
        },
      },
    );
  }

  return (
    <>
      <div className={`overlay ${open ? "on" : ""}`} onClick={onClose} />
      <aside className={`drawer ${open ? "on" : ""}`} aria-hidden={!open}>
        {ticket && (
          <>
            <div className="dh">
              <div>
                <div className="tk">
                  {ticket.id}
                  {ticket.sprint_id ? ` · ${ticket.sprint_id}` : ""}
                  {ticket.epic_id ? ` · ${ticket.epic_id}` : ""}
                </div>
                <h3>{ticket.title}</h3>
              </div>
              <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <span className={`pill ${statusToken(ticket.status).pill}`}>
                  {t(`tickets.statusLabel.${ticket.status}`)}
                </span>
                <button className="x" onClick={onClose}>
                  ✕
                </button>
              </div>
            </div>

            <div className="db">
              {/* Lane (assignee) + repo — la meta que JIRA muestra arriba del fold */}
              {(ticket.owner || ticket.repo) && (
                <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap", marginBottom: 14 }}>
                  {ticket.owner && <LaneChip lane={ticket.owner} title={t("tickets.detail.laneTitle")} />}
                  {ticket.repo && (
                    <span className="tag" style={{ fontSize: 11 }} title={ticket.repo}>
                      {ticket.repo.replace(/^https?:\/\/(www\.)?github\.com\//, "")}
                    </span>
                  )}
                </div>
              )}

              {/* Descripción / historia de usuario */}
              <div className="eyebrow acc">{t("tickets.detail.description")}</div>
              {ticket.body ? (
                <p className="td-body">{ticket.body}</p>
              ) : (
                <p className="td-empty">{t("tickets.detail.noDescription")}</p>
              )}

              {/* Criterios de aceptación */}
              {acs.length > 0 && (
                <>
                  <div className="eyebrow acc" style={{ marginTop: 20 }}>
                    {t("tickets.detail.acceptance")} · {acs.length}
                  </div>
                  <ul className="td-acs">
                    {acs.map((ac, i) => (
                      <li key={i}>
                        <span className="td-ac-mark">✓</span>
                        {ac}
                      </li>
                    ))}
                  </ul>
                </>
              )}

              {/* Dependencias — clickeables: navegar el grafo sin cerrar el drawer */}
              {ticket.deps && ticket.deps.length > 0 && (
                <>
                  <div className="eyebrow acc" style={{ marginTop: 20 }}>
                    {t("tickets.detail.dependencies")}
                  </div>
                  <div className="td-deps">
                    {ticket.deps.map((d) => (
                      <button
                        key={d}
                        className="td-dep click"
                        onClick={() => onOpenTicket(d)}
                        title={t("tickets.rowTitle")}
                      >
                        {d} →
                      </button>
                    ))}
                  </div>
                </>
              )}

              {/* Resultado + acciones: PR directo, ejecución, y recuperación de
                  una story fallida SIN el viaje ticket→run→requeue. */}
              <div style={{ marginTop: 24, display: "flex", flexDirection: "column", gap: 8 }}>
                {/* Enviar a GitHub: solo si la story no está aún espejada (sin
                    external_ref) ni tiene PR. Tras exportar, el ▶ de dispatch la ejecuta. */}
                {!ticket.external_ref && !ticket.pr_url && (
                  <button
                    className="btn primary"
                    style={{ width: "100%" }}
                    onClick={doExport}
                    disabled={exportStory.isPending}
                    title={t("tickets.detail.exportTitle")}
                  >
                    {exportStory.isPending ? t("tickets.detail.exporting") : t("tickets.detail.export")}
                  </button>
                )}
                {exportedRef && (
                  <a
                    className="btn ghost"
                    style={{ width: "100%", textAlign: "center" }}
                    href={issueUrl(exportedRef) ?? undefined}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {t("tickets.detail.viewIssue")}
                  </a>
                )}
                {candidate && onDispatch && (
                  <button
                    className="btn primary"
                    style={{ width: "100%" }}
                    onClick={() => onDispatch(candidate)}
                    title={t("tickets.dispatch.buttonTitle", {
                      executor: candidate.executor,
                      model: candidate.model || "auto",
                    })}
                  >
                    {candidate.kind === "sprint"
                      ? t("tickets.dispatch.detailSprint", { id: candidate.id, n: candidate.stories?.length ?? 0 })
                      : t("tickets.dispatch.detailStory")}
                  </button>
                )}
                {ticket.session_url && (
                  <a
                    className="btn primary"
                    style={{ width: "100%", textAlign: "center" }}
                    href={ticket.session_url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {t("tickets.detail.viewSession")}
                  </a>
                )}
                {ticket.pr_url && (
                  <a
                    className="btn ghost"
                    style={{ width: "100%", textAlign: "center" }}
                    href={ticket.pr_url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {t("tickets.detail.viewPR")}
                  </a>
                )}
                {/* Run legacy del kernel: solo cuando NO hay sesión GitHub (una
                    story espejada con run_id viejo confunde más de lo que aporta). */}
                {ticket.run_id && !ticket.session_url ? (
                  <button className="btn ghost" style={{ width: "100%" }} onClick={() => onOpenRun(ticket.run_id as string)}>
                    {t("tickets.detail.viewRun")}
                  </button>
                ) : !ticket.session_url ? (
                  <div className="td-empty">{t("tickets.detail.notRun")}</div>
                ) : null}
                {/* Reencolar: solo una story `failed` es reencolable (running/
                    in_review están vivas, done ya mergeó). Espejada en GitHub →
                    requeue nativo (vuelve a backlog + limpia la sesión de agente).
                    El backend valida: un run vivo devuelve 409 con su razón, que
                    mostramos inline. */}
                {ticket.external_ref && ticket.status === "failed" && (
                  <button
                    className="btn ghost"
                    style={{ width: "100%", color: "var(--danger)" }}
                    onClick={doRequeueStory}
                    disabled={requeueStory.isPending}
                    title={t("tickets.detail.requeueTitle")}
                  >
                    {requeueStory.isPending ? t("tickets.detail.requeueing") : t("tickets.detail.requeue")}
                  </button>
                )}
                {!ticket.external_ref && ticket.status === "failed" && ticket.run_id && (
                  <button
                    className="btn ghost"
                    style={{ width: "100%", color: "var(--danger)" }}
                    onClick={() => requeue.mutate([ticket.run_id as string], { onSuccess: onClose })}
                    disabled={requeue.isPending}
                    title={t("tickets.detail.requeueTitle")}
                  >
                    {requeue.isPending ? t("tickets.detail.requeueing") : t("tickets.detail.requeue")}
                  </button>
                )}

                {actionError && (
                  <div
                    style={{
                      padding: "8px 12px",
                      background: "var(--accent-soft)",
                      border: "1px solid var(--accent-line)",
                      borderRadius: 4,
                      fontSize: 12,
                      color: "var(--accent)",
                    }}
                  >
                    {actionError}
                  </div>
                )}

                {/* Eliminar la story (local, no toca GitHub) — acción destructiva
                    con confirm, separada del resto. */}
                <button
                  className="btn ghost"
                  style={{ width: "100%", color: "var(--danger)", marginTop: 8 }}
                  onClick={doDelete}
                  disabled={deleteStory.isPending}
                  title={t("tickets.detail.deleteTitle")}
                >
                  {deleteStory.isPending ? t("tickets.detail.deleting") : t("tickets.detail.delete")}
                </button>
              </div>
            </div>
          </>
        )}
      </aside>
    </>
  );
}
