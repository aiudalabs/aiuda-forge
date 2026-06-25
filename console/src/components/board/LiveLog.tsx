// Live-log: stream de eventos del bus (doc 16 §2.1/§4). Colorea por tipo, igual que el mockup.

import type { RunEvent, RunEventType } from "@/lib/types";

function typeClass(t: RunEventType): string {
  if (t === "run.created" || t === "step.gate" || t === "run.done") return "ok";
  if (t === "run.awaiting_approval" || t === "run.failed" || t === "run.cancelled") return "aw";
  return "st"; // step.status_changed, step.event, step.verify
}

export function LiveLog({ events, style }: { events: RunEvent[]; style?: React.CSSProperties }) {
  if (events.length === 0) {
    return (
      <div className="livelog" style={style}>
        <div>
          <span className="k">···</span> esperando eventos del bus…
        </div>
      </div>
    );
  }
  return (
    <div className="livelog" style={style}>
      {events.map((e) => (
        <div key={e.id}>
          <span className="k">{e.ts}</span> <span className={typeClass(e.type)}>{e.type}</span>{" "}
          {e.message}
        </div>
      ))}
    </div>
  );
}
