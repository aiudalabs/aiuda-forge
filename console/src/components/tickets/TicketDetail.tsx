"use client";

// TicketDetail (JIRA-like) — the STORY itself, not its execution. Opens for ANY
// ticket (clickable regardless of run state): id, sprint, lane, epic, status, the
// user-story description, falsifiable acceptance criteria, dependencies (clickable
// → navigate the graph), the PR when one exists, and the recovery action (requeue)
// when the story failed — the user shouldn't need to dig 3 levels to unblock work.
// If the story has a run, a button drops one level deeper into the execution.

import { useRequeue } from "@/lib/hooks";
import type { OrchestratorTicket } from "@/lib/types";
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

export function TicketDetail({
  ticket,
  onClose,
  onOpenTicket,
  onOpenRun,
}: {
  ticket: OrchestratorTicket | null;
  onClose: () => void;
  onOpenTicket: (id: string) => void;
  onOpenRun: (runId: string) => void;
}) {
  const t = useT();
  const requeue = useRequeue();
  const open = !!ticket;
  const acs = acceptanceLines(ticket?.acceptance);

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
                {ticket.run_id ? (
                  <button className="btn primary" style={{ width: "100%" }} onClick={() => onOpenRun(ticket.run_id as string)}>
                    {t("tickets.detail.viewRun")}
                  </button>
                ) : (
                  <div className="td-empty">{t("tickets.detail.notRun")}</div>
                )}
                {ticket.status === "failed" && ticket.run_id && (
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
              </div>
            </div>
          </>
        )}
      </aside>
    </>
  );
}
