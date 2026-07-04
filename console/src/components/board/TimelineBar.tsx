// TimelineBar: barra horizontal proporcional que resume el stream de eventos de un
// run por tipo (thinking / tool / text / system / …). Puramente visual — sin
// interactividad. Reutiliza los colores del LiveLog vía la clase por kind.

"use client";

import type { RunEvent } from "@/lib/types";
import { eventKind, kindClass } from "./LiveLog";

export function TimelineBar({ events }: { events: RunEvent[] }) {
  if (events.length === 0) return null;

  const counts = new Map<string, number>();
  for (const e of events) {
    const kind = eventKind(e);
    counts.set(kind, (counts.get(kind) ?? 0) + 1);
  }

  const total = events.length;
  const segments = [...counts.entries()];

  return (
    <div className="timeline-bar">
      {segments.map(([kind, count]) => (
        <div
          key={kind}
          className={`timeline-seg ${kindClass(kind)}`}
          style={{ width: `${(count / total) * 100}%` }}
          title={`${kind}: ${count}`}
        />
      ))}
    </div>
  );
}
