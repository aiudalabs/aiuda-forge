"use client";

// TicketDetail (JIRA-like) — the STORY itself, not its execution. Opens for ANY
// ticket (clickable regardless of run state): id, sprint, status, the user-story
// description, falsifiable acceptance criteria, and dependencies. If the story has a
// run, a button drops one level deeper into the execution (the run drawer).

import type { OrchestratorTicket, TicketStatus } from "@/lib/types";

const STATUS_LABEL: Record<TicketStatus, string> = {
  backlog: "Backlog",
  ready: "Listo",
  running: "En ejecución",
  in_review: "En revisión",
  done: "Done",
  failed: "Fallido",
};

const STATUS_CLASS: Record<TicketStatus, string> = {
  backlog: "queued",
  ready: "run_",
  running: "run_",
  in_review: "queued",
  done: "done",
  failed: "fail",
};

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
  onOpenRun,
}: {
  ticket: OrchestratorTicket | null;
  onClose: () => void;
  onOpenRun: (runId: string) => void;
}) {
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
                </div>
                <h3>{ticket.title}</h3>
              </div>
              <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <span className={`pill ${STATUS_CLASS[ticket.status]}`}>{STATUS_LABEL[ticket.status]}</span>
                <button className="x" onClick={onClose}>
                  ✕
                </button>
              </div>
            </div>

            <div className="db">
              {/* Descripción / historia de usuario */}
              <div className="eyebrow acc">Descripción</div>
              {ticket.body ? (
                <p className="td-body">{ticket.body}</p>
              ) : (
                <p className="td-empty">Sin descripción. El detalle dev-ready se genera al ejecutar la story.</p>
              )}

              {/* Criterios de aceptación */}
              {acs.length > 0 && (
                <>
                  <div className="eyebrow acc" style={{ marginTop: 20 }}>
                    Criterios de aceptación
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

              {/* Dependencias */}
              {ticket.deps && ticket.deps.length > 0 && (
                <>
                  <div className="eyebrow acc" style={{ marginTop: 20 }}>
                    Dependencias
                  </div>
                  <div className="td-deps">
                    {ticket.deps.map((d) => (
                      <span key={d} className="td-dep">
                        {d}
                      </span>
                    ))}
                  </div>
                </>
              )}

              {/* Enlace a la ejecución (un nivel más abajo) */}
              <div style={{ marginTop: 24 }}>
                {ticket.run_id ? (
                  <button className="btn primary" style={{ width: "100%" }} onClick={() => onOpenRun(ticket.run_id as string)}>
                    Ver ejecución →
                  </button>
                ) : (
                  <div className="td-empty">
                    Esta story aún no se ha ejecutado. Cuando el orquestador la dispare, aquí verás su run.
                  </div>
                )}
              </div>
            </div>
          </>
        )}
      </aside>
    </>
  );
}
