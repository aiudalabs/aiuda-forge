"use client";

// SprintsView — the project's PLAN as sprints (JIRA Backlog style). Each sprint is a
// collapsible section: name + goal (otherwise invisible in the UI), a progress bar,
// status, and its stories (clickable → ticket detail). A not-yet-started sprint shows
// what it's waiting on. Sprint metadata (name/goal) comes from /sprints; the stories
// + progress come from the project-scoped ticket list (so it's scoped to the project).

import { useMemo, useState } from "react";
import type { OrchestratorTicket, Sprint, TicketStatus } from "@/lib/types";
import { useRequeueSprint, useSprints } from "@/lib/hooks";
import { ApiError, type RequeueSprintResult } from "@/lib/api";
import { LaneChip } from "@/components/tickets/LaneChip";
import { statusToken } from "@/lib/statusToken";
import { useT } from "@/lib/i18n";

// Dominant status of a sprint: a problem/active state wins so it's visible.
function sprintState(statuses: TicketStatus[]): TicketStatus {
  for (const s of ["failed", "running", "in_review", "ready", "backlog", "done"] as TicketStatus[]) {
    if (statuses.includes(s)) return s;
  }
  return "done";
}

function sprintNum(id: string): number {
  const n = parseInt(id.replace(/\D/g, ""), 10);
  return isNaN(n) ? 999 : n;
}

interface SprintGroup {
  id: string;
  name: string;
  goal: string;
  stories: OrchestratorTicket[];
  done: number;
  state: TicketStatus;
  waitingOn: string[]; // other sprints this one depends on that aren't done
}

export function SprintsView({
  tickets,
  onOpenTicket,
}: {
  tickets: OrchestratorTicket[];
  onOpenTicket: (id: string) => void;
}) {
  const t = useT();
  const { data: sprintMeta } = useSprints();
  const requeueSprint = useRequeueSprint();
  // Per-sprint requeue feedback: the result to report, or an error message. Keyed
  // by sprint id so each card shows only its own outcome. pendingId disables just
  // the sprint being requeued (not every card).
  const [pendingId, setPendingId] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<Record<string, RequeueSprintResult | { error: string }>>({});

  function doRequeueSprint(id: string, failedCount: number) {
    if (!window.confirm(t("tickets.sprints.requeueConfirm", { n: failedCount }))) return;
    setPendingId(id);
    setFeedback((f) => {
      const next = { ...f };
      delete next[id];
      return next;
    });
    requeueSprint.mutate(id, {
      onSuccess: (r) => setFeedback((f) => ({ ...f, [id]: r })),
      onError: (e) => {
        const msg = e instanceof ApiError && e.detail ? e.detail : e instanceof Error ? e.message : String(e);
        setFeedback((f) => ({ ...f, [id]: { error: msg } }));
      },
      onSettled: () => setPendingId(null),
    });
  }

  const groups = useMemo<SprintGroup[]>(() => {
    const metaById = new Map((sprintMeta ?? []).map((s: Sprint) => [s.id, s]));
    const sprintOf = new Map(tickets.map((t) => [t.id, t.sprint_id || ""]));
    const doneIds = new Set(tickets.filter((t) => t.status === "done").map((t) => t.id));

    const by = new Map<string, OrchestratorTicket[]>();
    for (const t of tickets) {
      const sid = t.sprint_id || "—";
      (by.get(sid) ?? by.set(sid, []).get(sid)!).push(t);
    }

    const out: SprintGroup[] = [];
    for (const [id, stories] of by) {
      // cross-sprint deps not yet done → "waiting on".
      const waiting = new Set<string>();
      for (const t of stories) {
        for (const d of t.deps ?? []) {
          const depSprint = sprintOf.get(d);
          if (depSprint && depSprint !== id && !doneIds.has(d)) waiting.add(depSprint);
        }
      }
      const meta = metaById.get(id);
      out.push({
        id,
        name: meta?.name ?? id,
        goal: meta?.goal ?? "",
        stories,
        done: stories.filter((t) => t.status === "done").length,
        state: sprintState(stories.map((t) => t.status)),
        waitingOn: [...waiting].sort((a, b) => sprintNum(a) - sprintNum(b)),
      });
    }
    return out.sort((a, b) => sprintNum(a.id) - sprintNum(b.id));
  }, [tickets, sprintMeta]);

  // Collapse done sprints by default; keep the rest open (focus on what's pending).
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const isOpen = (g: SprintGroup) => (g.id in collapsed ? !collapsed[g.id] : g.state !== "done");

  if (groups.length === 0) {
    return <div className="placeholder">{t("tickets.sprints.empty")}</div>;
  }

  return (
    <div className="sprints">
      {groups.map((g) => {
        const open = isOpen(g);
        const pct = g.stories.length ? Math.round((g.done / g.stories.length) * 100) : 0;
        return (
          <section key={g.id} className="sprint-card">
            <button
              className="sprint-head"
              onClick={() => setCollapsed((c) => ({ ...c, [g.id]: open }))}
              aria-expanded={open}
            >
              <span className="sprint-caret">{open ? "▾" : "▸"}</span>
              <span className="sprint-id">{g.id}</span>
              <span className="sprint-name">{g.name.replace(/^Sprint\s+\d+\s*[—-]\s*/, "")}</span>
              <span className="sprint-bar">
                <span className={`sprint-fill st-${g.state}`} style={{ width: `${pct}%` }} />
              </span>
              <span className="sprint-count">
                {g.done}/{g.stories.length}
              </span>
              <span className={`sprint-tag st-${g.state}`}>{t(`tickets.statusLabel.${g.state}`)}</span>
            </button>

            {open && (
              <div className="sprint-body">
                {g.goal && <p className="sprint-goal">🎯 {g.goal}</p>}
                {g.waitingOn.length > 0 && g.state !== "done" && (
                  <p className="sprint-waiting">{t("tickets.sprints.waitingOn", { list: g.waitingOn.join(", ") })}</p>
                )}
                {(() => {
                  const failedCount = g.stories.filter((s) => s.status === "failed").length;
                  if (failedCount === 0) return null;
                  const fb = feedback[g.id];
                  return (
                    <div className="sprint-requeue">
                      <button
                        className="btn ghost"
                        style={{ color: "var(--danger)" }}
                        onClick={() => doRequeueSprint(g.id, failedCount)}
                        disabled={pendingId === g.id}
                        title={t("tickets.sprints.requeueTitle")}
                      >
                        {pendingId === g.id
                          ? t("tickets.detail.requeueing")
                          : t("tickets.sprints.requeueFailed", { n: failedCount })}
                      </button>
                      {fb && "error" in fb && <span className="sprint-requeue-msg err">{fb.error}</span>}
                      {fb && !("error" in fb) && (
                        <span className="sprint-requeue-msg">
                          {t("tickets.sprints.requeueDone", { n: fb.requeued.length })}
                          {fb.skipped.length > 0 &&
                            ` · ${t("tickets.sprints.requeueSkipped", { n: fb.skipped.length })}: ${fb.skipped
                              .map((s) => `${s.id} (${s.reason})`)
                              .join("; ")}`}
                        </span>
                      )}
                    </div>
                  );
                })()}
                <div className="sprint-stories">
                  {g.stories.map((story) => {
                    // Un "Listo" dentro de un sprint que espera a otros es engañoso
                    // (#21): el orquestador no lo va a disparar — se muestra apagado.
                    const gated =
                      g.waitingOn.length > 0 && (story.status === "ready" || story.status === "backlog");
                    return (
                      <button key={story.id} className="sprint-story" onClick={() => onOpenTicket(story.id)}>
                        <span className="sprint-story-id">{story.id}</span>
                        <span className="sprint-story-ttl">{story.title}</span>
                        {story.owner && <LaneChip lane={story.owner} />}
                        <span
                          className={`pill ${statusToken(story.status).pill}`}
                          style={gated ? { opacity: 0.55 } : undefined}
                          title={gated ? t("tickets.sprints.waitingOn", { list: g.waitingOn.join(", ") }) : undefined}
                        >
                          {t(`tickets.statusLabel.${story.status}`)}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
}

