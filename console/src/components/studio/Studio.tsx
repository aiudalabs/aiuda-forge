"use client";

// Studio shell (U1): two tabs for the active project —
//   · Especificación — the persistent specs read from the repo (Confluence-like).
//   · Diseño — the live design-run timeline (phases, gates, approvals).
// Especificación is the default: the spec is the thing that's always there, even
// after a design run has finished or been purged.

import { useState } from "react";
import { StudioDocs } from "./StudioDocs";
import { StudioView } from "./StudioView";

type Tab = "spec" | "design";

export function Studio() {
  const [tab, setTab] = useState<Tab>("spec");

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
