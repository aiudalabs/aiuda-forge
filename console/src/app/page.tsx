"use client";

import { useRouter } from "next/navigation";
import { StudioEntry } from "@/components/studio/StudioEntry";

// Home = the entry point: "what do you want to build?" — the place to start a new
// project. Launching one lands you on /studio (its spec + inline design pipeline),
// which is where the sidebar's Studio link points once you have a project.
export default function HomePage() {
  const router = useRouter();
  return <StudioEntry onLaunched={() => router.push("/studio")} />;
}
