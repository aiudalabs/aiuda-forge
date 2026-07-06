"use client";

// Studio (/studio) — a single view: the Especificación (StudioDocs), the repo's
// persistent docs, with the design pipeline (phases/gates/live generation) appearing
// INLINE as a state whenever there's an active design run. No project yet → bounce to
// the home entry ("/"), which is where a new project gets created.

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useActiveProject } from "@/lib/activeProject";
import { StudioDocs } from "./StudioDocs";

export function Studio() {
  const { project, isLoading } = useActiveProject();
  const router = useRouter();

  useEffect(() => {
    if (!isLoading && !project) router.replace("/");
  }, [isLoading, project, router]);

  if (!isLoading && !project) return null; // redirecting to the entry
  return <StudioDocs />;
}
