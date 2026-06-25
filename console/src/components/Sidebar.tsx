"use client";

// Sidebar: marca arriba, navegación (Board·Tickets·Studio·Registry·Gasto·Settings), selector de
// proyecto abajo. Navegable con rutas reales del App Router. Estado activo por pathname.

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Logo } from "./Logo";
import { SECTIONS, sectionForPath } from "@/lib/sections";
import { DEFAULT_PROJECT } from "@/lib/api";

export function Sidebar() {
  const path = usePathname();
  const active = sectionForPath(path).key;

  return (
    <aside className="nav">
      <Logo />
      <div className="navsec">Fábrica</div>
      <nav>
        {SECTIONS.map((s) => (
          <Link key={s.key} href={s.href} className={active === s.key ? "on" : ""}>
            <span className="ic">{s.icon}</span>
            <span className="lbl">{s.label}</span>
          </Link>
        ))}
      </nav>
      {/* TODO(endpoint): GET /projects para el selector multi-tenant (doc 16 §1). */}
      <div className="proj">
        <div className="eyebrow">Proyecto</div>
        <div className="nm">
          <span>{DEFAULT_PROJECT}</span>
          <span>▾</span>
        </div>
      </div>
    </aside>
  );
}
