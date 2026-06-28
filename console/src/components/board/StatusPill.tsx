"use client";

import type { RunStatus } from "@/lib/types";
import { useT } from "@/lib/i18n";

const MAP: Record<RunStatus, { cls: string; key: string }> = {
  RUNNING: { cls: "run_", key: "board.status.running" },
  AWAITING: { cls: "await", key: "board.status.awaiting" },
  DONE: { cls: "done", key: "board.status.done" },
  QUEUED: { cls: "queued", key: "board.status.queued" },
  FAILED: { cls: "fail", key: "board.status.failed" },
  CANCELLED: { cls: "queued", key: "board.status.cancelled" },
};

export function StatusPill({ status }: { status: RunStatus }) {
  const t = useT();
  const m = MAP[status] ?? MAP.QUEUED;
  return <span className={`pill ${m.cls}`}>{t(m.key)}</span>;
}
