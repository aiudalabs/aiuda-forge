"use client";

// Studio shell (U1 + U5 + C6b).
//   · No project yet → the conversational entry (StudioEntry): the obvious place to
//     start a new project. Launching it drops you straight into the design flow.
//   · With a project → a single view, the Especificación (StudioDocs): the repo's
//     persistent docs, with the design pipeline (phases/gates/live generation)
//     appearing INLINE as a state whenever there's an active design run — no more
//     separate "Diseño" tab to hunt for the gate you need to approve.

import { useActiveProject } from "@/lib/activeProject";
import { StudioDocs } from "./StudioDocs";
import { StudioEntry } from "./StudioEntry";

export function Studio() {
  const { project, isLoading } = useActiveProject();

  // No project → the conversational entry. Launching a design drops the user
  // straight into StudioDocs, where the freshly-created run shows up inline
  // (setActiveId makes it the active project, so no navigation is needed here).
  if (!isLoading && !project) {
    return <StudioEntry />;
  }

  return <StudioDocs />;
}
