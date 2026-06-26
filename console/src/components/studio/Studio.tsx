"use client";

// Studio shell (U1 + U5).
//   · No project yet → the conversational entry (StudioEntry): the obvious place to
//     start a new project. Launching it drops you straight into the Diseño flow.
//   · With a project → two tabs:
//       Especificación — the persistent specs read from the repo (Confluence-like).
//       Diseño — the live design-run timeline (phases, gates, approvals).

import { useState } from "react";
import { useActiveProject } from "@/lib/activeProject";
import { StudioDocs } from "./StudioDocs";
import { StudioEntry } from "./StudioEntry";
import { StudioView } from "./StudioView";

type Tab = "spec" | "design";

export function Studio() {
  const { project, isLoading } = useActiveProject();
  const [tab, setTab] = useState<Tab>("spec");

  // No project → the conversational entry. Launching a design auto-routes to the
  // Diseño tab so the user lands on the live flow instead of hunting for it.
  if (!isLoading && !project) {
    return <StudioEntry onLaunched={() => setTab("design")} />;
  }

  return (
    <>
      <div className="studio-tabs">
        <button className={`studio-tab${tab === "spec" ? " on" : ""}`} onClick={() => setTab("spec")}>
          Especificación
        </button>
        <button className={`studio-tab${tab === "design" ? " on" : ""}`} onClick={() => setTab("design")}>
          Diseño
        </button>
      </div>
      {tab === "spec" ? <StudioDocs /> : <StudioView />}
    </>
  );
}
