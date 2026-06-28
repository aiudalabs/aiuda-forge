"use client";

// CANALES + IMPORT (v1.3). Vincular tu Telegram (genera un código que envías al
// bot), gestionar los chats que reciben notificaciones del proyecto, y disparar el
// import de issues de GitHub al backlog. Gestión = editor+ (el backend lo gatea).

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import * as api from "@/lib/api";
import { useActiveProject } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";

export function ChannelsPanel() {
  const { activeId } = useActiveProject();
  const qc = useQueryClient();
  const t = useT();

  const channelsQuery = useQuery({
    queryKey: ["channels", activeId],
    queryFn: () => api.listChannels(activeId as string),
    enabled: !!activeId,
  });
  const channels = channelsQuery.data ?? [];

  const [chatId, setChatId] = useState("");
  const [code, setCode] = useState<string | null>(null);
  const [importMsg, setImportMsg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = () => qc.invalidateQueries({ queryKey: ["channels", activeId] });

  const linkCode = useMutation({
    mutationFn: () => api.issueLinkCode("telegram"),
    onSuccess: (res) => {
      setError(null);
      setCode(res.code);
    },
    onError: (e) => setError(e instanceof Error ? e.message : "error"),
  });

  const addChannel = useMutation({
    mutationFn: () => api.linkChannel(activeId as string, "telegram", chatId.trim(), "*"),
    onSuccess: () => {
      setError(null);
      setChatId("");
      refresh();
    },
    onError: (e) => setError(e instanceof Error ? e.message : "error"),
  });

  const removeChannel = useMutation({
    mutationFn: (target: string) => api.unlinkChannel(activeId as string, "telegram", target),
    onSuccess: refresh,
    onError: (e) => setError(e instanceof Error ? e.message : "error"),
  });

  const importGh = useMutation({
    mutationFn: () => api.importGitHub(activeId as string),
    onSuccess: (res) => {
      setError(null);
      setImportMsg(t("import.result", { imported: res.imported, skipped: res.skipped }));
    },
    onError: (e) => setError(e instanceof Error ? e.message : t("import.failed")),
  });

  if (!activeId) return null;

  return (
    <div className="cwrap">
      {/* Channels */}
      <section className="card">
        <div className="sectitle">
          <h3>{t("chan.title")}</h3>
          <span className="c">{t("chan.subtitle")}</span>
        </div>

        <button className="btn ghost" disabled={linkCode.isPending} onClick={() => linkCode.mutate()}>
          {t("chan.linkTelegram")}
        </button>
        {code && (
          <div className="code">
            <span>{t("chan.linkCodeHint")}</span>
            <code>/link {code}</code>
            <button
              className="link"
              onClick={() => navigator.clipboard?.writeText(`/link ${code}`).catch(() => {})}
            >
              {t("chan.copyCode")}
            </button>
          </div>
        )}

        <div className="addrow">
          <input
            className="inp"
            placeholder={t("chan.chatId")}
            value={chatId}
            onChange={(e) => setChatId(e.target.value)}
          />
          <button className="btn" disabled={!chatId.trim() || addChannel.isPending} onClick={() => addChannel.mutate()}>
            {t("chan.addChannel")}
          </button>
        </div>

        <h4>{t("chan.linked")} ({channels.length})</h4>
        {channels.length === 0 ? (
          <div className="muted">{t("chan.none")}</div>
        ) : (
          <ul className="list">
            {channels.map((ch) => (
              <li key={ch.connector + ch.target} className="row">
                <span className="badge">{ch.connector}</span>
                <span className="target">{ch.target}</span>
                <span className="muted">{ch.events}</span>
                <button className="link danger" onClick={() => removeChannel.mutate(ch.target)}>
                  {t("chan.remove")}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Import */}
      <section className="card">
        <div className="sectitle">
          <h3>{t("import.title")}</h3>
          <span className="c">{t("import.subtitle")}</span>
        </div>
        <button className="btn" disabled={importGh.isPending} onClick={() => importGh.mutate()}>
          {importGh.isPending ? t("import.importing") : t("import.github")}
        </button>
        {importMsg && <div className="ok">{importMsg}</div>}
      </section>

      {error && <div className="err">{error}</div>}

      <style jsx>{`
        .cwrap { display: flex; flex-direction: column; gap: 16px; max-width: 720px; margin: 0 24px 24px; }
        .card { background: #fff; border: 1px solid #eee; border-radius: 14px; padding: 18px; display: flex; flex-direction: column; gap: 12px; }
        .sectitle h3 { margin: 0; font-size: 14px; font-weight: 800; }
        .sectitle .c { color: #888; font-size: 13px; }
        h4 { margin: 6px 0 0; font-size: 13px; font-weight: 700; color: #555; }
        .btn { align-self: flex-start; padding: 9px 16px; border: none; border-radius: 9px; background: #E8440A; color: #fff; font-weight: 700; cursor: pointer; }
        .btn.ghost { background: #fff; color: #E8440A; border: 1px solid #E8440A; }
        .btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .code { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; font-size: 13px; color: #555; background: #faf8f4; border: 1px solid #eee; border-radius: 9px; padding: 10px 12px; }
        .code code { font-weight: 700; color: #222; }
        .addrow { display: flex; gap: 8px; }
        .inp { box-sizing: border-box; flex: 1 1 auto; min-width: 200px; padding: 9px 11px; border: 1px solid #ddd; border-radius: 9px; font-size: 14px; outline: none; }
        .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
        .row { display: flex; align-items: center; gap: 12px; padding: 6px 0; border-bottom: 1px solid #f4f4f4; }
        .row:last-child { border-bottom: none; }
        .badge { font-size: 12px; font-weight: 700; padding: 3px 9px; border-radius: 999px; background: #e7f0fd; color: #2563c0; }
        .target { flex: 1; font-size: 14px; }
        .muted { color: #999; font-size: 13px; }
        .link { background: none; border: none; color: #2563c0; font-size: 13px; cursor: pointer; padding: 0; }
        .link.danger { color: #c0392b; }
        .ok { background: #f0f8f0; color: #2a6; border: 1px solid #cce8cc; border-radius: 10px; padding: 8px 12px; font-size: 13px; max-width: 720px; margin: 0 24px; }
        .err { background: #fdf0ec; color: #E8440A; border: 1px solid #f5d5c8; border-radius: 10px; padding: 8px 12px; font-size: 13px; max-width: 720px; margin: 0 24px; }
      `}</style>
    </div>
  );
}
