"use client";

// Overview (U2) — the project home: specs (Confluence) + flow (JIRA) at a glance.
// One screen to answer "what are we building?" (the spec docs) and "where is it?"
// (sprint progress, what's running, spend) without hopping between tabs.

import Link from "next/link";
import { useMemo } from "react";
import type { OrchestratorTicket, TicketStatus } from "@/lib/types";
import { useActiveProject } from "@/lib/activeProject";
import { useMetrics, useProjectDocs, useTickets } from "@/lib/hooks";

const DOC_TITLE: Record<string, string> = {
  "BRIEF.md": "Brief",
  "PRD.md": "PRD · Requisitos",
  "ARCHITECTURE.md": "Arquitectura",
  "UI_SCREENS.md": "Pantallas UI",
  "backlog.yaml": "Backlog",
  mockups: "Mockups",
  "SESSION.md": "Sesión de diseño",
};

const STATUS_LABEL: Record<TicketStatus, string> = {
  backlog: "Backlog",
  ready: "Listo",
  running: "En ejecución",
  in_review: "En revisión",
  done: "Done",
  failed: "Fallido",
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
          <span className="spin" /> cargando…
        </div>
      </div>
    );
  }
  if (!project) {
    return (
      <div className="wrap">
        <div className="placeholder">
          Sin proyecto activo.{" "}
          <Link href="/studio" className="acc">
            Empieza uno en Studio →
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
          <div className="eyebrow acc">Resumen del proyecto</div>
          <h2 className="ov-title">
            {project.name}
            {project.repo && (
              <a className="ov-repo" href={project.repo} target="_blank" rel="noreferrer">
                ↗ repo
              </a>
            )}
          </h2>
          {project.description && <p className="ov-desc">{project.description}</p>}
        </div>
        <div className="ov-progress">
          <div className="ov-progress-top">
            <span className="ov-progress-pct serif">{stats.pct}%</span>
            <span className="ov-progress-sub">
              {stats.done} / {stats.total} stories
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
          <div className="eyebrow">Sprints completos</div>
          <div className="n serif">
            {sprintsDone}
            <span className="ov-of"> / {stats.sprints.length}</span>
          </div>
        </div>
        <div className="stat">
          <div className="eyebrow">En ejecución</div>
          <div className="n serif" style={{ color: "var(--navy)" }}>{stats.running}</div>
          <div className="sub">stories construyéndose</div>
        </div>
        <div className="stat">
          <div className="eyebrow">En revisión</div>
          <div className="n serif" style={{ color: "#7a5d00" }}>{stats.inReview}</div>
          <div className="sub">esperando merge</div>
        </div>
        <div className="stat">
          <div className="eyebrow">Gasto</div>
          <div className="n serif acc">${(metrics?.total_cost_usd ?? 0).toFixed(2)}</div>
          <div className="sub">{metrics?.agent_calls ?? 0} llamadas</div>
        </div>
      </div>

      {/* Two columns: spec + flow */}
      <div className="ov-cols">
        {/* Especificación */}
        <section className="ov-card">
          <div className="ov-card-head">
            <span>Especificación</span>
            <Link href="/studio" className="ov-link">
              Abrir Studio →
            </Link>
          </div>
          {docFiles.length === 0 ? (
            <div className="ov-empty">Specs aún no generados (fase de diseño).</div>
          ) : (
            <div className="ov-doclist">
              {docFiles.map((d) => (
                <Link key={d.path} href="/studio" className="ov-docitem">
                  <span className="ov-docname">{DOC_TITLE[d.name] ?? d.name}</span>
                  <span className="ov-docarrow">→</span>
                </Link>
              ))}
            </div>
          )}
        </section>

        {/* Flujo */}
        <section className="ov-card">
          <div className="ov-card-head">
            <span>Flujo · sprints</span>
            <Link href="/tickets" className="ov-link">
              Abrir Board →
            </Link>
          </div>
          {stats.sprints.length === 0 ? (
            <div className="ov-empty">Sin backlog todavía.</div>
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
                  <span className={`ov-sprint-tag st-${s.state}`}>{STATUS_LABEL[s.state]}</span>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
