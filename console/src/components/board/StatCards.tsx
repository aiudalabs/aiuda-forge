// Stat cards del board (doc 16 §2.1): en ejecución · esperan aprobación · PRs abiertos · costo.

"use client";

import { useStats } from "@/lib/hooks";
import { useActiveProjectId } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";

export function StatCards() {
  const t = useT();
  const projectId = useActiveProjectId();
  const { data, isLoading } = useStats(projectId);
  const s = data ?? { running: 0, awaiting: 0, openPRs: 0, projectCost: 0 };

  return (
    <div className="stats">
      <div className="stat">
        <div className="eyebrow">{t("board.stat.running")}</div>
        <div className="n acc">{isLoading ? "—" : s.running}</div>
        <div className="sub">{t("board.stat.running.sub")}</div>
      </div>
      <div className="stat">
        <div className="eyebrow">{t("board.stat.awaiting")}</div>
        <div className="n">{isLoading ? "—" : s.awaiting}</div>
        <div className="sub">{t("board.stat.awaiting.sub")}</div>
      </div>
      <div className="stat">
        <div className="eyebrow">{t("board.stat.openPRs")}</div>
        <div className="n em">{isLoading ? "—" : s.openPRs}</div>
        <div className="sub">{t("board.stat.openPRs.sub")}</div>
      </div>
      <div className="stat">
        <div className="eyebrow">{t("board.stat.cost")}</div>
        <div className="n serif">${s.projectCost.toFixed(2)}</div>
        <div className="sub">{t("board.stat.cost.sub")}</div>
      </div>
    </div>
  );
}
