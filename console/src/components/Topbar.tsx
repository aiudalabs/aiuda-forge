"use client";

// Topbar: eyebrow + título (por sección) · costo del día · badge de modo (mock/real) ·
// campana con contador de notificaciones (awaiting/failed) + popover · avatar.

import { useRouter, usePathname } from "next/navigation";
import { useState } from "react";
import { sectionForPath } from "@/lib/sections";
import { useApiMode, useNotifications, useSpendToday } from "@/lib/hooks";
import { useActiveProjectId } from "@/lib/activeProject";
import { logout } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { LanguageSwitcher } from "./LanguageSwitcher";

export function Topbar() {
  const path = usePathname();
  const router = useRouter();
  const section = sectionForPath(path);
  const t = useT();
  const projectId = useActiveProjectId();
  const { data: spend } = useSpendToday(projectId);
  const { data: mode } = useApiMode();
  const { data: notifications = [] } = useNotifications(projectId);
  const [open, setOpen] = useState(false);

  const count = notifications.length;

  return (
    <header className="top">
      <div>
        <div className="eyebrow acc">{t(`nav.${section.key}.eyebrow`)}</div>
        <div className="h1">{t(`nav.${section.key}.title`)}</div>
      </div>
      <div className="sp" />

      <LanguageSwitcher />

      {mode && (
        <span className={`modebadge ${mode}`} title={mode === "mock" ? t("top.mockHint") : t("top.liveHint")}>
          {mode === "mock" ? t("top.mock") : t("top.live")}
        </span>
      )}

      <div className="cost">
        <span className="dot" /> {t("top.today")} <b>${(spend?.cost ?? 0).toFixed(2)}</b> ·{" "}
        <b className="mono">{spend?.tokens ?? "—"} {t("top.tokens")}</b>
      </div>

      <button
        className="bell"
        title={count ? `${count} ${t("top.needsAttention")}` : t("top.none")}
        onClick={() => setOpen((o) => !o)}
      >
        🔔{count > 0 && <span className="badge-n">{count}</span>}
      </button>

      {open && (
        <div className="notif-pop" onMouseLeave={() => setOpen(false)}>
          <div className="nh">{t("top.notifications")}</div>
          {count === 0 ? (
            <div className="notif-empty">{t("top.noNotifications")}</div>
          ) : (
            notifications.map((n) => (
              <div
                key={n.id}
                className="notif-item"
                onClick={() => {
                  setOpen(false);
                  // Lleva al board con el run abierto (el board lee ?run=).
                  router.push(`/?run=${n.runId}`);
                }}
              >
                <span className={`ndot ${n.kind}`} />
                <div>
                  <div style={{ fontWeight: 600 }}>{n.title}</div>
                  <div style={{ color: "var(--ink4)", fontSize: 12 }}>
                    {n.kind === "awaiting" ? t("top.awaiting") : t("top.failed")} · {n.ts}
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      )}

      <button
        className="ava"
        title={t("top.logout")}
        onClick={async () => {
          await logout();
          window.location.href = "/login";
        }}
      >
        NM
      </button>
    </header>
  );
}
