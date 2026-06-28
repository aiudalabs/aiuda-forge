"use client";

// Overview (U2) — the project home: specs (Confluence) + flow (JIRA) at a glance.
// One screen to answer "what are we building?" (the spec docs) and "where is it?"
// (sprint progress, what's running, spend) without hopping between tabs.

import Link from "next/link";
import { useMemo } from "react";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";
import { useActiveProject } from "@/lib/activeProject";
import { useMetrics, useProjectDocs, useTickets } from "@/lib/hooks";
import { useT } from "@/lib/i18n";

// Doc file/name → i18n key ("overview.doc.*"). Resolved with t() at render.
const DOC_TITLE_KEY: Record<string, string> = {
  "BRIEF.md": "overview.doc.brief",
  "PRD.md": "overview.doc.prd",
  "ARCHITECTURE.md": "overview.doc.architecture",
  "UI_SCREENS.md": "overview.doc.uiScreens",
  "backlog.yaml": "overview.doc.backlog",
  mockups: "overview.doc.mockups",
  "SESSION.md": "overview.doc.session",
};

// Ticket status → i18n key ("overview.status.*"). Resolved with t() at render.
const STATUS_LABEL_KEY: Record<TicketStatus, string> = {
  backlog: "overview.status.backlog",
  ready: "overview.status.ready",
  running: "overview.status.running",
  in_review: "overview.status.in_review",
  done: "overview.status.done",
  failed: "overview.status.failed",
};

// Dominant status of a sprint (for the row's accent), worst-to-best precedence so a
// problem is visible: failed > running > in_review > backlog/ready > done.
function sprintState(statuses: TicketStatus[]): TicketStatus {
  const order: TicketStatus[] = ["failed", "running", "in_review", "ready", "backlog", "done"];
  for (const s of order) if (statuses.includes(s)) return s;
  return "done";
}

interface SprintRow {
  id: string;
  total: number;
  done: number;
  state: TicketStatus;
}

function sprintRows(tickets: OrchestratorTicket[]): SprintRow[] {
  const by = new Map<string, OrchestratorTicket[]>();
  for (const t of tickets) {
    const sid = t.sprint_id || "—";
    (by.get(sid) ?? by.set(sid, []).get(sid)!).push(t);
  }
  const rows = [...by.entries()].map(([id, ts]) => ({
    id,
    total: ts.length,
    done: ts.filter((t) => t.status === "done").length,
    state: sprintState(ts.map((t) => t.status)),
  }));
  // SP1, SP2, … numeric sort; non-sprint buckets last.
  return rows.sort((a, b) => {
    const na = parseInt(a.id.replace(/\D/g, ""), 10);
    const nb = parseInt(b.id.replace(/\D/g, ""), 10);
    if (isNaN(na)) return 1;
    if (isNaN(nb)) return -1;
    return na - nb;
  });
}

export function Overview() {
  const t = useT();
  const { project, isLoading: projLoading } = useActiveProject();
  const projectId = project?.id ?? null;
  const { data: tickets } = useTickets(projectId);
  const { data: metrics } = useMetrics(projectId);
  const { data: docs } = useProjectDocs(projectId);

  const stats = useMemo(() => {
    const list = tickets ?? [];
    const count = (s: TicketStatus) => list.filter((t) => t.status === s).length;
    const done = count("done");
    return {
      total: list.length,
      done,
      running: count("running"),
      inReview: count("in_review"),
      failed: count("failed"),
      pct: list.length ? Math.round((done / list.length) * 100) : 0,
      sprints: sprintRows(list),
    };
  }, [tickets]);

  const sprintsDone = stats.sprints.filter((s) => s.state === "done" && s.total > 0).length;
  const docFiles = (docs ?? []).filter((d) => d.type === "file" || d.name === "mockups");

  if (projLoading) {
    return (
      <div className="wrap">
        <div className="placeholder">
          <span className="spin" /> {t("overview.loading")}
        </div>
      </div>
    );
  }
  if (!project) {
    return (
      <div className="wrap">
        <div className="placeholder">
          {t("overview.noProject")}{" "}
          <Link href="/studio" className="acc">
            {t("overview.startInStudio")}
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="wrap">
      {/* Hero */}
      <div className="ov-hero">
        <div>
          <div className="eyebrow acc">{t("overview.eyebrow")}</div>
          <h2 className="ov-title">
            {project.name}
            {project.repo && (
              <a className="ov-repo" href={project.repo} target="_blank" rel="noreferrer">
                {t("overview.repo")}
              </a>
            )}
          </h2>
          {project.description && <p className="ov-desc">{project.description}</p>}
        </div>
        <div className="ov-progress">
          <div className="ov-progress-top">
            <span className="ov-progress-pct serif">{stats.pct}%</span>
            <span className="ov-progress-sub">
              {t("overview.stories", { done: stats.done, total: stats.total })}
            </span>
          </div>
          <div className="ov-bar">
            <div className="ov-bar-fill" style={{ width: `${stats.pct}%` }} />
          </div>
        </div>
      </div>

      {/* KPIs */}
      <div className="stats">
        <div className="stat">
          <div className="eyebrow">{t("overview.kpi.sprintsDone")}</div>
          <div className="n serif">
            {sprintsDone}
            <span className="ov-of"> / {stats.sprints.length}</span>
          </div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("overview.kpi.running")}</div>
          <div className="n serif" style={{ color: "var(--navy)" }}>{stats.running}</div>
          <div className="sub">{t("overview.kpi.running.sub")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("overview.kpi.inReview")}</div>
          <div className="n serif" style={{ color: "#7a5d00" }}>{stats.inReview}</div>
          <div className="sub">{t("overview.kpi.inReview.sub")}</div>
        </div>
        <div className="stat">
          <div className="eyebrow">{t("overview.kpi.spend")}</div>
          <div className="n serif acc">${(metrics?.total_cost_usd ?? 0).toFixed(2)}</div>
          <div className="sub">{t("overview.kpi.spend.sub", { n: metrics?.agent_calls ?? 0 })}</div>
        </div>
      </div>

      {/* Two columns: spec + flow */}
      <div className="ov-cols">
        {/* Especificación */}
        <section className="ov-card">
          <div className="ov-card-head">
            <span>{t("overview.spec")}</span>
            <Link href="/studio" className="ov-link">
              {t("overview.spec.open")}
            </Link>
          </div>
          {docFiles.length === 0 ? (
            <div className="ov-empty">{t("overview.spec.empty")}</div>
          ) : (
            <div className="ov-doclist">
              {docFiles.map((d) => (
                <Link key={d.path} href="/studio" className="ov-docitem">
                  <span className="ov-docname">{DOC_TITLE_KEY[d.name] ? t(DOC_TITLE_KEY[d.name]) : d.name}</span>
                  <span className="ov-docarrow">→</span>
                </Link>
              ))}
            </div>
          )}
        </section>

        {/* Flujo */}
        <section className="ov-card">
          <div className="ov-card-head">
            <span>{t("overview.flow")}</span>
            <Link href="/tickets" className="ov-link">
              {t("overview.flow.open")}
            </Link>
          </div>
          {stats.sprints.length === 0 ? (
            <div className="ov-empty">{t("overview.flow.empty")}</div>
          ) : (
            <div className="ov-sprints">
              {stats.sprints.map((s) => (
                <div key={s.id} className="ov-sprint">
                  <span className="ov-sprint-id">{s.id}</span>
                  <div className="ov-sprint-bar">
                    <div
                      className={`ov-sprint-fill st-${s.state}`}
                      style={{ width: `${s.total ? (s.done / s.total) * 100 : 0}%` }}
                    />
                  </div>
                  <span className="ov-sprint-count">
                    {s.done}/{s.total}
                  </span>
                  <span className={`ov-sprint-tag st-${s.state}`}>{t(STATUS_LABEL_KEY[s.state])}</span>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
