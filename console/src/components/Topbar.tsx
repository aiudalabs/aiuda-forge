"use client";

// Topbar: eyebrow + título (por sección) · costo del día · badge de modo (mock/real) ·
// campana con contador de notificaciones (awaiting/failed) + popover · avatar.

import { useRouter, usePathname } from "next/navigation";
import { useState } from "react";
import { sectionForPath } from "@/lib/sections";
import { useApiMode, useNotifications, useSpendToday } from "@/lib/hooks";
import { useActiveProjectId } from "@/lib/activeProject";
import { logout } from "@/lib/auth";

export function Topbar() {
  const path = usePathname();
  const router = useRouter();
  const section = sectionForPath(path);
  const projectId = useActiveProjectId();
  const { data: spend } = useSpendToday(projectId);
  const { data: mode } = useApiMode();
  const { data: notifications = [] } = useNotifications(projectId);
  const [open, setOpen] = useState(false);

  const count = notifications.length;

  return (
    <header className="top">
      <div>
        <div className="eyebrow acc">{section.eyebrow}</div>
        <div className="h1">{section.title}</div>
      </div>
      <div className="sp" />

      {mode && (
        <span className={`modebadge ${mode}`} title={mode === "mock" ? "Datos de ejemplo (API no detectada)" : "Conectado al control-plane"}>
          {mode === "mock" ? "● mock" : "● en vivo"}
        </span>
      )}

      <div className="cost">
        <span className="dot" /> hoy <b>${(spend?.cost ?? 0).toFixed(2)}</b> ·{" "}
        <b className="mono">{spend?.tokens ?? "—"} tok</b>
      </div>

      <button
        className="bell"
        title={count ? `${count} corrida(s) requieren atención` : "Sin notificaciones"}
        onClick={() => setOpen((o) => !o)}
      >
        🔔{count > 0 && <span className="badge-n">{count}</span>}
      </button>

      {open && (
        <div className="notif-pop" onMouseLeave={() => setOpen(false)}>
          <div className="nh">Notificaciones</div>
          {count === 0 ? (
            <div className="notif-empty">Nada requiere tu atención.</div>
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
                    {n.kind === "awaiting" ? "Espera aprobación" : "Falló"} · {n.ts}
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      )}

      <button
        className="ava"
        title="Cerrar sesión"
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
