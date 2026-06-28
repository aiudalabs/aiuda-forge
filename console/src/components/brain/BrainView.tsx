"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useActiveProjectId } from "@/lib/activeProject";
import { subscribe } from "@/lib/ws";
import {
  sendAssistantMessage,
  getAssistantHistory,
  approveAction,
  rejectAction,
  type BrainMessage,
  type ProposedAction,
} from "@/lib/brain";

// BrainView is the per-project conversational assistant. It POSTs the user's
// message, then renders the assistant's reply streamed over the WS bus
// (assistant.token), surfacing proposed mutating actions (assistant.action) as
// Approve/Reject cards until the turn ends (assistant.done).
export function BrainView() {
  const projectId = useActiveProjectId();
  const [messages, setMessages] = useState<BrainMessage[]>([]);
  const [streaming, setStreaming] = useState<string>("");
  const [actions, setActions] = useState<ProposedAction[]>([]);
  const [busy, setBusy] = useState(false);
  const [input, setInput] = useState("");
  const [error, setError] = useState<string | null>(null);

  const convId = useRef<string>("");
  const scroller = useRef<HTMLDivElement>(null);

  // Load the persisted conversation when the project changes.
  useEffect(() => {
    if (!projectId) return;
    let cancelled = false;
    getAssistantHistory(projectId)
      .then((h) => {
        if (cancelled) return;
        convId.current = h.conversation_id;
        setMessages(h.messages ?? []);
      })
      .catch((e) => !cancelled && setError(String(e)));
    return () => {
      cancelled = true;
    };
  }, [projectId]);

  // Stream assistant events over the shared WS bus, filtered to this conversation.
  useEffect(() => {
    const off = subscribe((ev: { runId: string; type: string; data?: Record<string, unknown> }) => {
      if (!ev.type?.startsWith("assistant.") || ev.runId !== convId.current) return;
      const d = ev.data ?? {};
      if (ev.type === "assistant.token") {
        const t = typeof d.text === "string" ? d.text : "";
        if (t) setStreaming((prev) => prev + t);
      } else if (ev.type === "assistant.action") {
        setActions((prev) => [
          ...prev,
          {
            action_id: String(d.action_id ?? ""),
            tool: String(d.tool ?? ""),
            args: (d.args as Record<string, unknown>) ?? {},
          },
        ]);
      } else if (ev.type === "assistant.done") {
        setBusy(false);
        setStreaming((cur) => {
          if (cur.trim()) setMessages((prev) => [...prev, { role: "assistant", content: cur }]);
          return "";
        });
        const errMsg = d.error;
        if (typeof errMsg === "string" && errMsg) setError(errMsg);
      }
    });
    return off;
  }, []);

  // Autoscroll to the newest content.
  useEffect(() => {
    scroller.current?.scrollTo({ top: scroller.current.scrollHeight, behavior: "smooth" });
  }, [messages, streaming, actions]);

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || !projectId || busy) return;
    setError(null);
    setBusy(true);
    setInput("");
    setMessages((prev) => [...prev, { role: "user", content: text }]);
    try {
      const r = await sendAssistantMessage(projectId, text);
      convId.current = r.conversation_id;
    } catch (e) {
      setBusy(false);
      setError(String(e));
    }
  }, [input, projectId, busy]);

  const resolve = useCallback(
    async (a: ProposedAction, approve: boolean) => {
      if (!projectId) return;
      try {
        await (approve ? approveAction : rejectAction)(projectId, a.action_id);
      } catch (e) {
        setError(String(e));
      } finally {
        setActions((prev) => prev.filter((x) => x.action_id !== a.action_id));
      }
    },
    [projectId],
  );

  if (!projectId) {
    return <div className="brain"><p className="brain-empty">Selecciona un proyecto para hablar con el Brain.</p></div>;
  }

  return (
    <div className="brain">
      <div className="brain-inner">
        <div ref={scroller} className="brain-scroll">
          {messages.length === 0 && !streaming && (
            <p className="brain-empty">
              Soy el Brain de este proyecto. Pídeme: <em>&ldquo;resume el estado&rdquo;</em>,{" "}
              <em>&ldquo;para la ejecución&rdquo;</em>, <em>&ldquo;¿por qué falló el último run?&rdquo;</em> o{" "}
              <em>&ldquo;agrega tal funcionalidad&rdquo;</em>.
            </p>
          )}
          {messages.map((m, i) => (
            <Bubble key={i} role={m.role} content={m.content} />
          ))}
          {streaming && <Bubble role="assistant" content={streaming} />}
          {actions.map((a) => (
            <ActionCard key={a.action_id} action={a} onResolve={resolve} />
          ))}
          {busy && !streaming && <p className="brain-think">· pensando…</p>}
          {error && <p className="brain-err">{error}</p>}
        </div>

        <form
          className="brain-form"
          onSubmit={(e) => {
            e.preventDefault();
            void send();
          }}
        >
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
            placeholder="Escribe un mensaje…  (Enter envía · Shift+Enter salto de línea)"
            rows={2}
          />
          <button type="submit" className="btn primary" disabled={busy || !input.trim()}>
            Enviar
          </button>
        </form>
      </div>
    </div>
  );
}

function Bubble({ role, content }: { role: string; content: string }) {
  const isUser = role === "user";
  return (
    <div className={`brain-row ${isUser ? "user" : "assistant"}`}>
      <div className={`brain-bubble ${isUser ? "user" : "assistant"}`}>
        {isUser ? content : <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>}
      </div>
    </div>
  );
}

function ActionCard({ action, onResolve }: { action: ProposedAction; onResolve: (a: ProposedAction, approve: boolean) => void }) {
  return (
    <div className="brain-action">
      <div className="h">
        El Brain propone una acción <span className="tool">{action.tool}</span>
      </div>
      <pre>{JSON.stringify(action.args, null, 2)}</pre>
      <div className="acts">
        <button className="btn primary sm" onClick={() => onResolve(action, true)}>
          Aprobar
        </button>
        <button className="btn ghost sm" onClick={() => onResolve(action, false)}>
          Rechazar
        </button>
      </div>
    </div>
  );
}
