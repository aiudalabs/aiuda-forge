"use client";

// Sidebar: marca arriba, navegación (Board·Tickets·Studio·Registry·Gasto·Settings),
// SELECTOR DE PROYECTO abajo (multi-tenant, Wave 2). El proyecto activo scopea toda la
// consola; vive en useActiveProject (localStorage + GET /projects). Estado activo por pathname.

import Link from "next/link";
import { useRouter } from "next/navigation";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { Logo } from "./Logo";
import { SECTIONS, sectionForPath } from "@/lib/sections";
import { useActiveProject } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";

export function Sidebar() {
  const path = usePathname();
  const active = sectionForPath(path).key;
  const t = useT();

  return (
    <aside className="nav">
      <Logo />
      <div className="navsec">{t("nav.factory")}</div>
      <nav>
        {SECTIONS.map((s) => (
          <Link key={s.key} href={s.href} className={active === s.key ? "on" : ""}>
            <span className="ic">{s.icon}</span>
            <span className="lbl">{t(`nav.${s.key}.label`)}</span>
          </Link>
        ))}
      </nav>
      <ProjectSwitcher />
    </aside>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Selector de proyecto (multi-tenant)
// ─────────────────────────────────────────────────────────────────────────────

function ProjectSwitcher() {
  const router = useRouter();
  const { project, projects, setActiveId, isLoading } = useActiveProject();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const t = useT();

  // Cerrar al hacer clic fuera o con Escape.
  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Sin proyectos: invitar a crear el primero (lo crea el modal del Studio).
  if (!isLoading && projects.length === 0) {
    return (
      <div className="proj" onClick={() => router.push("/")} role="button" tabIndex={0}>
        <div className="eyebrow">{t("nav.project")}</div>
        <div className="nm">
          <span style={{ color: "var(--ink4)" }}>{t("nav.createFirst")}</span>
        </div>
      </div>
    );
  }

  const label = project?.name ?? (isLoading ? t("nav.loading") : t("nav.selectProject"));

  return (
    <div className="proj-switch" ref={rootRef}>
      {open && (
        <div className="proj-menu" role="listbox">
          {projects.map((p) => (
            <button
              key={p.id}
              role="option"
              aria-selected={p.id === project?.id}
              className={`proj-opt${p.id === project?.id ? " on" : ""}`}
              onClick={() => {
                setActiveId(p.id);
                setOpen(false);
              }}
            >
              <span className="proj-opt-name">{p.name}</span>
              {p.id === project?.id && <span className="proj-opt-check">✓</span>}
            </button>
          ))}
        </div>
      )}
      <div
        className="proj"
        onClick={() => setOpen((o) => !o)}
        role="button"
        tabIndex={0}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        <div className="eyebrow">{t("nav.project")}</div>
        <div className="nm">
          <span>{label}</span>
          <span>▾</span>
        </div>
      </div>
    </div>
  );
}
