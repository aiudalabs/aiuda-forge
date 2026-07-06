"use client";

// Studio shell (U1 + U5).
//   · No project yet → the conversational entry (StudioEntry): the obvious place to
//     start a new project. Launching it drops you straight into the Diseño flow.
//   · With a project → two tabs:
//       Especificación — the persistent specs read from the repo (Confluence-like).
//       Diseño — the live design-run timeline (phases, gates, approvals).

import { useState } from "react";
import { useActiveProject } from "@/lib/activeProject";
import { useActiveDesignRun } from "@/lib/hooks";
import { useT } from "@/lib/i18n";
import { StudioDocs } from "./StudioDocs";
import { StudioEntry } from "./StudioEntry";
import { StudioView } from "./StudioView";

type Tab = "spec" | "design";

export function Studio() {
  const { project, isLoading } = useActiveProject();
  const [tab, setTab] = useState<Tab>("spec");
  const t = useT();
  const activeRun = useActiveDesignRun(project?.id ?? null);
  const awaitingCount = (activeRun?.phases ?? []).filter((p) => p.gateStatus === "AWAITING").length;

  // No project → the conversational entry. Launching a design auto-routes to the
  // Diseño tab so the user lands on the live flow instead of hunting for it.
  if (!isLoading && !project) {
    return <StudioEntry onLaunched={() => setTab("design")} />;
  }

  return (
    <>
      <div className="studio-tabs">
        <button className={`studio-tab${tab === "spec" ? " on" : ""}`} onClick={() => setTab("spec")}>
          {t("studio.tab.spec")}
        </button>
        <button className={`studio-tab${tab === "design" ? " on" : ""}`} onClick={() => setTab("design")}>
          {t("studio.tab.design")}
          {activeRun && (
            <span
              style={{ marginLeft: 6, width: 7, height: 7, borderRadius: "50%", background: awaitingCount > 0 ? "var(--accent)" : "var(--navy)", display: "inline-block", verticalAlign: "middle" }}
            />
          )}
        </button>
      </div>
      {tab === "spec" ? <StudioDocs onOpenPipeline={() => setTab("design")} /> : <StudioView />}
    </>
  );
}
