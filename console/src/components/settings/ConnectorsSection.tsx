"use client";

// CONECTORES (Settings) — config de canales en UN solo lugar, estilo n8n:
//  · guía de setup + enlace a la doc de BotFather
//  · token del bot (instancia) con Guardar
//  · chats del proyecto (vincular tu Telegram con código · añadir chat id · quitar)
//  · botón Probar que envía un mensaje real por el conector
// El token se guarda en settings.mcp.telegram.token (reusa GET/PUT /settings con el
// payload completo para no pisar otras conexiones). Reemplaza el flujo confuso de
// "token en MCP + chat en Equipo".

import { useEffect, useMemo, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import * as api from "@/lib/api";
import { useSettings, useSaveSettings } from "@/lib/hooks";
import { useActiveProject } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";
import type { McpConnection, SettingsPayload } from "@/lib/types";

const MASKED = "••••••••";
const BOTFATHER_DOCS = "https://core.telegram.org/bots/features#botfather";

function telegramConn(s?: SettingsPayload): McpConnection | undefined {
  return s?.mcp?.find((c) => c.name.toLowerCase() === "telegram");
}

export function ConnectorsSection() {
  const t = useT();
  const { activeId } = useActiveProject();
  const qc = useQueryClient();

  const { data: settings } = useSettings();
  const saveSettings = useSaveSettings();

  const existing = useMemo(() => telegramConn(settings), [settings]);
  const connected = !!existing?.token; // any stored token (masked or not) = configured
  const [token, setToken] = useState("");
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Prefill the field with the masked sentinel when a token is already stored.
  useEffect(() => {
    if (existing?.token) setToken(MASKED);
  }, [existing?.token]);

  async function saveToken() {
    if (!settings) return;
    setError(null);
    setSaved(false);
    // Build the full mcp array with telegram's token updated (masked = unchanged).
    const others = (settings.mcp ?? []).filter((c) => c.name.toLowerCase() !== "telegram");
    const next: SettingsPayload = {
      ...settings,
      mcp: [...others, { name: "telegram", url: "", token: token || MASKED }],
    };
    try {
      await saveSettings.mutateAsync(next);
      setSaved(true);
      setTimeout(() => setSaved(false), 2500);
    } catch (e) {
      setError(e instanceof Error ? e.message : "error");
    }
  }

  // ── per-project channels ────────────────────────────────────────────────────
  const channelsQuery = useQuery({
    queryKey: ["channels", activeId],
    queryFn: () => api.listChannels(activeId as string),
    enabled: !!activeId,
  });
  const channels = channelsQuery.data ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: ["channels", activeId] });

  const [chatId, setChatId] = useState("");
  const [code, setCode] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const linkCode = useMutation({
    mutationFn: () => api.issueLinkCode("telegram"),
    onSuccess: (r) => setCode(r.code),
    onError: (e) => setError(e instanceof Error ? e.message : "error"),
  });
  const addChat = useMutation({
    mutationFn: () => api.linkChannel(activeId as string, "telegram", chatId.trim(), "*"),
    onSuccess: () => { setChatId(""); refresh(); },
    onError: (e) => setError(e instanceof Error ? e.message : "error"),
  });
  const removeChat = useMutation({
    mutationFn: (target: string) => api.unlinkChannel(activeId as string, "telegram", target),
    onSuccess: refresh,
    onError: (e) => setError(e instanceof Error ? e.message : "error"),
  });
  const test = useMutation({
    mutationFn: () => api.testChannels(activeId as string),
    onSuccess: (r) => {
      setError(null);
      setNotice(r.sent > 0 ? t("conn.testOk", { sent: r.sent }) : t("conn.testNoChannels"));
    },
    onError: (e) => setError(e instanceof Error ? e.message : "error"),
  });

  return (
    <div className="cwrap">
      <div className="sectitle">
        <h2>{t("conn.title")}</h2>
        <span className="c">{t("conn.subtitle")}</span>
      </div>

      <section className="card">
        <div className="head">
          <h3>{t("conn.telegram")}</h3>
          <span className={connected ? "st on" : "st"}>
            {connected ? t("conn.status.connected") : t("conn.status.off")}
          </span>
        </div>

        <details className="guide">
          <summary>{t("conn.guide")}</summary>
          <ol>
            <li>{t("conn.step1")}</li>
            <li>{t("conn.step2")}</li>
            <li>{t("conn.step3")}</li>
            <li>{t("conn.step4")}</li>
          </ol>
          <a href={BOTFATHER_DOCS} target="_blank" rel="noreferrer" className="link">
            {t("conn.guideDocs")} ↗
          </a>
        </details>

        <label className="fld">
          {t("conn.botToken")}
          <div className="row">
            <input
              type="password"
              className="inp"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="123456:ABC-..."
            />
            <button className="btn" disabled={saveSettings.isPending} onClick={saveToken}>
              {saved ? t("conn.saved") : t("conn.save")}
            </button>
          </div>
        </label>

        {/* chats */}
        <h4>{t("conn.chats")}{activeId ? "" : ""}</h4>
        <div className="row">
          <button className="btn ghost" disabled={linkCode.isPending} onClick={() => linkCode.mutate()}>
            {t("chan.linkTelegram")}
          </button>
          {code && (
            <code className="codebox" onClick={() => navigator.clipboard?.writeText(`/link ${code}`).catch(() => {})}>
              /link {code}
            </code>
          )}
        </div>
        <div className="row">
          <input className="inp" placeholder={t("chan.chatId")} value={chatId} onChange={(e) => setChatId(e.target.value)} />
          <button className="btn" disabled={!chatId.trim() || addChat.isPending} onClick={() => addChat.mutate()}>
            {t("chan.addChannel")}
          </button>
          <button className="btn ghost" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? t("conn.testing") : t("conn.test")}
          </button>
        </div>

        {channels.length > 0 && (
          <ul className="list">
            {channels.map((ch) => (
              <li key={ch.target} className="litem">
                <span className="target">{ch.target}</span>
                <button className="link danger" onClick={() => removeChat.mutate(ch.target)}>
                  {t("chan.remove")}
                </button>
              </li>
            ))}
          </ul>
        )}

        {notice && <div className="msg ok">{notice}</div>}
        {error && <div className="msg err">{error}</div>}
      </section>

      <style jsx>{`
        .cwrap { max-width: 720px; margin: 0 24px 24px; display: flex; flex-direction: column; gap: 12px; }
        .sectitle h2 { margin: 0; font-weight: 900; font-size: 18px; }
        .sectitle .c { color: #888; font-size: 13px; }
        .card { background: #fff; border: 1px solid #eee; border-radius: 14px; padding: 18px; display: flex; flex-direction: column; gap: 12px; }
        .head { display: flex; align-items: center; justify-content: space-between; }
        .head h3 { margin: 0; font-size: 15px; font-weight: 800; }
        .st { font-size: 12px; color: #999; font-weight: 700; }
        .st.on { color: #2a6; }
        .guide { font-size: 13px; color: #555; background: #faf8f4; border: 1px solid #eee; border-radius: 9px; padding: 10px 12px; }
        .guide summary { cursor: pointer; font-weight: 700; color: #333; }
        .guide ol { margin: 8px 0; padding-left: 18px; display: flex; flex-direction: column; gap: 4px; }
        .fld { display: flex; flex-direction: column; gap: 6px; font-size: 13px; color: #555; }
        .row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
        .inp { box-sizing: border-box; flex: 1 1 auto; min-width: 200px; padding: 9px 11px; border: 1px solid #ddd; border-radius: 9px; font-size: 14px; outline: none; }
        h4 { margin: 6px 0 0; font-size: 13px; font-weight: 700; color: #555; }
        .btn { flex: 0 0 auto; padding: 9px 16px; border: none; border-radius: 9px; background: #E8440A; color: #fff; font-weight: 700; cursor: pointer; }
        .btn.ghost { background: #fff; color: #E8440A; border: 1px solid #E8440A; }
        .btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .codebox { font-size: 13px; font-weight: 700; color: #222; background: #faf8f4; border: 1px solid #eee; border-radius: 8px; padding: 6px 10px; cursor: pointer; }
        .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
        .litem { display: flex; align-items: center; gap: 12px; padding: 5px 0; border-bottom: 1px solid #f4f4f4; }
        .litem:last-child { border-bottom: none; }
        .target { flex: 1; font-size: 14px; }
        .link { background: none; border: none; color: #2563c0; font-size: 13px; cursor: pointer; padding: 0; }
        .link.danger { color: #c0392b; }
        .msg { font-size: 13px; padding: 8px 12px; border-radius: 9px; }
        .msg.ok { background: #f0f8f0; color: #2a6; border: 1px solid #cce8cc; }
        .msg.err { background: #fdf0ec; color: #E8440A; border: 1px solid #f5d5c8; }
      `}</style>
    </div>
  );
}
