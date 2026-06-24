// Metadatos de las secciones — el sidebar y la topbar leen de aquí. Títulos/eyebrows tomados
// del mockup (doc 15) y del spec (doc 16 §1).

export interface SectionMeta {
  key: string;
  href: string;
  icon: string;
  label: string;
  eyebrow: string;
  title: string;
}

export const SECTIONS: SectionMeta[] = [
  { key: "board", href: "/", icon: "▦", label: "Board · Runs", eyebrow: "Ejecución autónoma", title: "Board · Runs" },
  { key: "tickets", href: "/tickets", icon: "☰", label: "Tickets", eyebrow: "Backlog · MCP", title: "Tickets" },
  { key: "studio", href: "/studio", icon: "✎", label: "Studio", eyebrow: "Diseño del producto", title: "Studio" },
  { key: "registry", href: "/registry", icon: "◆", label: "Registry", eyebrow: "Configuración · no-code", title: "Registry" },
  { key: "spend", href: "/spend", icon: "◷", label: "Gasto", eyebrow: "Observabilidad", title: "Gasto" },
  { key: "settings", href: "/settings", icon: "⚙", label: "Settings", eyebrow: "Configuración", title: "Settings" },
];

export function sectionForPath(path: string): SectionMeta {
  if (path === "/") return SECTIONS[0];
  const found = SECTIONS.find((s) => s.href !== "/" && path.startsWith(s.href));
  return found || SECTIONS[0];
}
