// Live-log: stream de eventos del bus — estilo terminal Multica.
// Cada evento es una fila colapsable: resumen de una línea, click para ver
// el contenido completo (JSON de tool input, texto completo del agente, etc.).
// Controles: filtro por tipo + sort oldest/newest-first.

"use client";

import { useEffect, useRef, useState } from "react";
import type { RunEvent } from "@/lib/types";
import { useT } from "@/lib/i18n";

// eventKind deriva el "kind" fino: para step.event es data.kind
// (thinking / tool_use / text / system); para el resto, el type del bus.
export function eventKind(e: RunEvent): string {
  if (e.type === "step.event") {
    const k = e.data?.kind;
    return typeof k === "string" && k ? k : "event";
  }
  return e.type;
}

// kindClass mapea un kind a la clase CSS de color (compartida con TimelineBar).
export function kindClass(kind: string): string {
  switch (kind) {
    case "thinking":
      return "log-thinking";
    case "tool_use":
    case "tool_result":
      return "log-tool";
    case "text":
      return "log-text";
    case "system":
      return "log-system";
    case "run.done":
    case "step.gate":
      return "log-ok";
    case "run.failed":
    case "run.cancelled":
      return "log-err";
    default:
      return "log-st";
  }
}

function badgeLabel(e: RunEvent, kind: string): string {
  if (kind === "tool_use") {
    const tool = e.data?.tool;
    return typeof tool === "string" && tool ? tool : "tool";
  }
  return kind;
}

// fullContent devuelve el contenido expandible completo de un evento.
// Retorna "" cuando no hay nada extra útil que mostrar.
function fullContent(e: RunEvent): string {
  if (e.type !== "step.event") return "";
  const d = e.data;
  if (!d) return "";
  const kind = d.kind as string;

  if (kind === "tool_use") {
    const input = d.input;
    if (!input) return "";
    try {
      const parsed = typeof input === "string" ? JSON.parse(input) : input;
      return JSON.stringify(parsed, null, 2);
    } catch {
      return typeof input === "string" ? input : "";
    }
  }

  if (kind === "text" || kind === "thinking") {
    return (d.text as string) || "";
  }

  if (kind === "tool_result") {
    return (d.output as string) || "";
  }

  return "";
}

const FILTER_KINDS = ["thinking", "tool_use", "text", "system"] as const;

export function LiveLog({ events, style }: { events: RunEvent[]; style?: React.CSSProperties }) {
  const t = useT();
  const [filter, setFilter] = useState("");
  const [newestFirst, setNewestFirst] = useState(false);
  const endRef = useRef<HTMLDivElement>(null);

  // Auto-scroll al final cuando llegan nuevos eventos (solo en oldest-first).
  useEffect(() => {
    if (!newestFirst) {
      endRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [events, newestFirst]);

  if (events.length === 0) {
    return (
      <div className="livelog" style={style}>
        <div className="log-empty">
          <span className="k">···</span> {t("board.log.waiting")}
        </div>
      </div>
    );
  }

  const displayed = (() => {
    const filtered = filter ? events.filter((e) => eventKind(e) === filter) : events;
    return newestFirst ? [...filtered].reverse() : filtered;
  })();

  return (
    <div className="livelog" style={style}>
      {/* Toolbar: filtro + sort. Ocupa una línea compacta dentro del terminal. */}
      <div className="log-controls">
        <select
          className="log-filter"
          value={filter}
          onChange={(ev) => setFilter(ev.target.value)}
          aria-label={t("board.log.filter")}
        >
          <option value="">{t("board.log.filterAll")}</option>
          {FILTER_KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
        <button
          className="log-sort-btn"
          onClick={() => setNewestFirst((v) => !v)}
        >
          {newestFirst ? "↑ newest" : "↓ oldest"}
        </button>
      </div>

      {/* Filas colapsables: resumen siempre visible, detail al hacer click. */}
      {displayed.map((e) => {
        const kind = eventKind(e);
        const cls = kindClass(kind);
        const badge = badgeLabel(e, kind);
        const detail = fullContent(e);

        if (detail) {
          return (
            <details key={e.id} className={`log-row ${cls}`}>
              <summary>
                <span className="k">{e.ts}</span>{" "}
                <span className={`log-badge log-badge-${kind}`}>{badge}</span>{" "}
                {e.message}
              </summary>
              <pre className="log-detail">{detail}</pre>
            </details>
          );
        }

        return (
          <div key={e.id} className={`log-row ${cls}`}>
            <span className="k">{e.ts}</span>{" "}
            <span className={`log-badge log-badge-${kind}`}>{badge}</span>{" "}
            {e.message}
          </div>
        );
      })}

      <div ref={endRef} />
    </div>
  );
}
