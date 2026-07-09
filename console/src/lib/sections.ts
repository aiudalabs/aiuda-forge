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
  { key: "studio", href: "/studio", icon: "✎", label: "Studio", eyebrow: "Diseño del producto", title: "Studio" },
  { key: "overview", href: "/overview", icon: "◇", label: "Resumen", eyebrow: "Vista del proyecto", title: "Resumen" },
  { key: "brain", href: "/brain", icon: "✦", label: "Brain", eyebrow: "Asistente del proyecto", title: "Brain" },
  { key: "tickets", href: "/tickets", icon: "☰", label: "Tickets", eyebrow: "Backlog · MCP", title: "Tickets" },
  { key: "flow", href: "/flow", icon: "⌥", label: "Flow", eyebrow: "El proyecto como grafo vivo", title: "Flow" },
  { key: "agents", href: "/agents", icon: "▶", label: "Agentes", eyebrow: "Sesiones · Pull Requests", title: "Agentes" },
  { key: "registry", href: "/registry", icon: "◆", label: "Registry", eyebrow: "Configuración · no-code", title: "Registry" },
  { key: "spend", href: "/spend", icon: "◷", label: "Gasto", eyebrow: "Observabilidad", title: "Gasto" },
  { key: "docs", href: "/docs", icon: "❏", label: "Docs", eyebrow: "Cómo funciona", title: "Docs" },
  { key: "team", href: "/team", icon: "◉", label: "Equipo", eyebrow: "Miembros · roles", title: "Equipo" },
  { key: "settings", href: "/settings", icon: "⚙", label: "Settings", eyebrow: "Configuración", title: "Settings" },
];

export function sectionForPath(path: string): SectionMeta {
  // Exact match first (handles "/" → Resumen and "/board" → Board), then prefix
  // match for nested routes (e.g. "/tickets/123" → Tickets).
  const exact = SECTIONS.find((s) => s.href === path);
  if (exact) return exact;
  const found = SECTIONS.find((s) => s.href !== "/" && path.startsWith(s.href));
  return found || SECTIONS[0];
}
