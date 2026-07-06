"use client";

// Studio (/studio) — a single view: the Especificación (StudioDocs), the repo's
// persistent docs, with the design pipeline (phases/gates/live generation) appearing
// INLINE as a state whenever there's an active design run. Only bounce to the home
// entry ("/") when the user genuinely has NO project — NOT while a just-created one is
// still loading into the projects list (activeId is set synchronously before we land
// here, so gating on it avoids the "new project redirects home" race).

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useActiveProject } from "@/lib/activeProject";
import { StudioDocs } from "./StudioDocs";

export function Studio() {
  const { activeId, projects, isLoading } = useActiveProject();
  const router = useRouter();

  const noProject = !isLoading && !activeId && projects.length === 0;

  useEffect(() => {
    if (noProject) router.replace("/");
  }, [noProject, router]);

  if (noProject) return null; // redirecting to the entry
  return <StudioDocs />;
}
