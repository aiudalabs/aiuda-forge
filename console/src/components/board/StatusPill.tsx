import type { RunStatus } from "@/lib/types";

const MAP: Record<RunStatus, { cls: string; label: string }> = {
  RUNNING: { cls: "run_", label: "Running" },
  AWAITING: { cls: "await", label: "Awaiting" },
  DONE: { cls: "done", label: "Done" },
  QUEUED: { cls: "queued", label: "Queued" },
  FAILED: { cls: "fail", label: "Failed" },
  CANCELLED: { cls: "queued", label: "Cancelled" },
};

export function StatusPill({ status }: { status: RunStatus }) {
  const m = MAP[status] ?? MAP.QUEUED;
  return <span className={`pill ${m.cls}`}>{m.label}</span>;
}
