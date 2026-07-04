"use client";

// AGENTES — la vista mission-control del pivote GitHub-native (PLAN §4). Reemplaza
// conceptualmente al viejo Board·Runs: en vez de runs del kernel propio, muestra
// (1) las SESIONES de agentes en vivo (stories corriendo en GitHub con session_url)
// y (2) la COLA de PRs abiertos, con las stories que los originaron y los workflow
// runs detenidos en action_required esperando aprobación humana.
//
// Sigue el shell JIRA-style de Tickets (tickets-shell/head/canvas): header delgado
// + canvas full-bleed que scrollea, sin cajas-widget. Los paneles agents-* viven
// en globals.css.

import { useMemo, useState } from "react";
import { useApproveWorkflow, useProjectPRs, useResolveConflicts, useTickets } from "@/lib/hooks";
import { useActiveProject, useActiveProjectId } from "@/lib/activeProject";
import { LaneChip } from "@/components/tickets/LaneChip";
import { useT } from "@/lib/i18n";
import type { ApproveWorkflowResult, OrchestratorTicket, ProjectPR } from "@/lib/types";

// ─────────────────────────────────────────────────────────────────────────────
// Sesión activa — una story corriendo/en revisión con sesión de agente en GitHub
// ─────────────────────────────────────────────────────────────────────────────

function SessionCard({ story }: { story: OrchestratorTicket }) {
  const t = useT();
  return (
    <div className="agent-card">
      <div className="agent-card-top">
        <span className="agent-card-id">{story.id}</span>
        {story.sprint_id && <span className="agent-card-sprint">{story.sprint_id}</span>}
      </div>
      <div className="agent-card-ttl">{story.title}</div>
      <div className="agent-card-meta">
        {story.owner && <LaneChip lane={story.owner} />}
        <span className="agent-live">
          <span className="agent-live-dot" />
          {t(`tickets.statusLabel.${story.status}`)}
        </span>
        <span className="sp" />
        {story.session_url && (
          <a
            className="agent-link"
            href={story.session_url}
            target="_blank"
            rel="noreferrer"
            title={t("agents.sessions.openSession")}
          >
            {t("agents.sessions.session")}
          </a>
        )}
        {story.pr_url && (
          <a
            className="agent-link"
            href={story.pr_url}
            target="_blank"
            rel="noreferrer"
            title={t("agents.sessions.openPR")}
          >
            {t("agents.sessions.pr")}
          </a>
        )}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Fila de PR — número, título, autor, draft/ready, stories ligadas + aprobación
// de sus workflow runs en action_required
// ─────────────────────────────────────────────────────────────────────────────

function PRRow({ pr, projectId }: { pr: ProjectPR; projectId: string }) {
  const t = useT();
  const approve = useApproveWorkflow(projectId);
  const resolve = useResolveConflicts(projectId);
  // Resultado por runId: un 409 (bloqueado por política) deja el reason a la vista.
  const [results, setResults] = useState<Record<string, ApproveWorkflowResult>>({});
  // Resultado de la resolución de conflicto (mensaje/estado bajo la fila).
  const [resolveMsg, setResolveMsg] = useState<string | null>(null);

  function runApprove(runId: string) {
    approve.mutate(runId, {
      onSuccess: (res) => setResults((prev) => ({ ...prev, [runId]: res })),
      onError: () =>
        setResults((prev) => ({
          ...prev,
          [runId]: { approved: false, safe: false, reason: t("agents.error") },
        })),
    });
  }

  function runResolve() {
    if (!window.confirm(t("agents.prs.resolveConfirm", { n: pr.number }))) return;
    setResolveMsg(null);
    resolve.mutate(pr.number, {
      onSuccess: (res) =>
        setResolveMsg(res.dispatched ? t("agents.prs.resolveDispatched") : res.reason || t("agents.prs.resolveInFlight")),
      onError: () => setResolveMsg(t("agents.prs.resolveError")),
    });
  }

  const runs = pr.action_required_runs ?? [];
  const pending = runs.filter((r) => !results[r.id]?.approved);
  const conflicting = pr.merge_state === "conflicting";

  return (
    <div className="pr-row">
      <div className="pr-main">
        <a
          className="pr-num"
          href={pr.url}
          target="_blank"
          rel="noreferrer"
          title={t("agents.prs.openPR")}
        >
          #{pr.number}
        </a>
        <span className="pr-ttl">{pr.title}</span>
        <span className={`pill ${pr.draft ? "queued" : "ready"}`} style={{ fontSize: 10, padding: "2px 8px" }}>
          {pr.draft ? t("agents.prs.draft") : t("agents.prs.ready")}
        </span>
        {conflicting && (
          <span className="pill fail" style={{ fontSize: 10, padding: "2px 8px" }} title={t("agents.prs.conflictHint")}>
            ⚠ {t("agents.prs.conflict")}
          </span>
        )}
        <span className="pr-author">@{pr.author}</span>
        <span className="sp" />
        {conflicting && (
          <button className="btn ghost sm" disabled={resolve.isPending} onClick={runResolve}>
            {resolve.isPending ? t("agents.prs.resolving") : t("agents.prs.resolve")}
          </button>
        )}
        <a className="agent-link" href={pr.url} target="_blank" rel="noreferrer" title={t("agents.prs.openPR")}>
          {t("agents.sessions.pr")}
        </a>
      </div>

      {resolveMsg && <div className="pr-resolve-msg c">{resolveMsg}</div>}

      {pr.stories.length > 0 && (
        <div className="pr-stories">
          <span className="pr-stories-lbl">{t("agents.prs.stories")}</span>
          {pr.stories.map((s) => (
            <span key={s} className="pr-story-chip">
              {s}
            </span>
          ))}
        </div>
      )}

      {runs.length > 0 && (
        <div className="pr-workflows">
          {pending.length > 1 && (
            <button
              className="btn ghost sm"
              disabled={approve.isPending}
              onClick={() => pending.forEach((r) => runApprove(r.id))}
            >
              {t("agents.prs.approveAll")}
            </button>
          )}
          {runs.map((r) => {
            const res = results[r.id];
            if (res?.approved) {
              return (
                <span key={r.id} className="pr-wf-ok">
                  ✓ {r.name} · {t("agents.prs.approved")}
                </span>
              );
            }
            return (
              <div key={r.id} className="pr-wf">
                <button
                  className="btn ghost sm"
                  disabled={approve.isPending}
                  onClick={() => runApprove(r.id)}
                >
                  {approve.isPending ? t("agents.prs.approving") : `${t("agents.prs.approve")} · ${r.name}`}
                </button>
                {res && !res.approved && (
                  <span className="pr-wf-blocked" title={res.reason || t("agents.prs.blockedDefault")}>
                    ⚠ {t("agents.prs.blocked")}: {res.reason || t("agents.prs.blockedDefault")}
                  </span>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Vista raíz
// ─────────────────────────────────────────────────────────────────────────────

export function AgentsView() {
  const t = useT();
  const projectId = useActiveProjectId();
  const { project } = useActiveProject();

  const ticketsQ = useTickets(projectId);
  const prsQ = useProjectPRs(projectId);

  // Sesiones activas = stories con sesión de agente en GitHub y estado vivo.
  const sessions = useMemo(
    () =>
      (ticketsQ.data ?? []).filter(
        (s) => s.session_url && (s.status === "running" || s.status === "in_review"),
      ),
    [ticketsQ.data],
  );
  const prs = prsQ.data ?? [];

  const refetchAll = () => {
    ticketsQ.refetch();
    prsQ.refetch();
  };

  return (
    <div className="tickets-shell">
      <div className="tickets-head">
        <h2>{t("agents.title")}</h2>
        <span className="c">{t("agents.desc")}</span>
        <span className="sp" />
        <button className="btn ghost sm" onClick={refetchAll}>
          {t("agents.refresh")}
        </button>
      </div>

      <div className="tickets-canvas pad">
        {/* ── Panel: sesiones activas ─────────────────────────────────────── */}
        <section className="agents-section">
          <div className="agents-section-head">
            <h3>{t("agents.sessions.title")}</h3>
            {sessions.length > 0 && (
              <span className="agents-count">{t("agents.sessions.count", { n: sessions.length })}</span>
            )}
          </div>
          {ticketsQ.isLoading ? (
            <div className="placeholder">
              <div className="ph-ic">
                <span className="spin" />
              </div>
              {t("agents.loading")}
            </div>
          ) : ticketsQ.isError ? (
            <div className="placeholder err">
              <div className="ph-ic">⚠</div>
              {t("agents.error")}{" "}
              <button className="btn ghost sm" onClick={() => ticketsQ.refetch()}>
                {t("agents.retry")}
              </button>
            </div>
          ) : sessions.length === 0 ? (
            <div className="agents-empty">{t("agents.sessions.empty")}</div>
          ) : (
            <div className="agents-grid">
              {sessions.map((s) => (
                <SessionCard key={s.id} story={s} />
              ))}
            </div>
          )}
        </section>

        {/* ── Panel: cola de PRs ──────────────────────────────────────────── */}
        <section className="agents-section">
          <div className="agents-section-head">
            <h3>{t("agents.prs.title")}</h3>
            {prs.length > 0 && (
              <span className="agents-count">{t("agents.prs.count", { n: prs.length })}</span>
            )}
          </div>
          {prsQ.isLoading ? (
            <div className="placeholder">
              <div className="ph-ic">
                <span className="spin" />
              </div>
              {t("agents.loading")}
            </div>
          ) : prsQ.isError ? (
            <div className="placeholder err">
              <div className="ph-ic">⚠</div>
              {t("agents.error")}{" "}
              <button className="btn ghost sm" onClick={() => prsQ.refetch()}>
                {t("agents.retry")}
              </button>
            </div>
          ) : prs.length === 0 ? (
            <div className="agents-empty">{t("agents.prs.empty")}</div>
          ) : (
            <div className="pr-list">
              {prs.map((pr) => (
                <PRRow key={pr.number} pr={pr} projectId={projectId ?? ""} />
              ))}
            </div>
          )}
        </section>

        {!project && <div className="agents-empty">{t("nav.selectProject")}</div>}
      </div>
    </div>
  );
}
