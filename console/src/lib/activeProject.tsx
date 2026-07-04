"use client";

// Proyecto activo (multi-tenant, Wave 2). La consola está scopeada a UN proyecto:
// Board/Runs, Tickets y Studio leen GET …?project=<id>. Aquí vive el id activo:
// persistido en localStorage para sobrevivir recargas, expuesto por contexto para
// que cualquier vista lo lea con useActiveProject() sin prop-drilling.
//
// Default: el primer proyecto del usuario (GET /projects). Si el usuario no tiene
// ninguno, activeId queda null y la UI invita a crear el primero (modal de Studio).

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { useProjects } from "./hooks";
import type { Project } from "./types";

const STORAGE_KEY = "vibeforge.activeProject";

interface ActiveProjectValue {
  /** Proyecto activo resuelto (o undefined mientras carga / si no hay ninguno). */
  project?: Project;
  /** Id activo (localStorage), o null si el usuario no tiene proyectos todavía. */
  activeId: string | null;
  /** Lista de proyectos del usuario (GET /projects). */
  projects: Project[];
  /** Cambia el proyecto activo y lo persiste. */
  setActiveId: (id: string) => void;
  isLoading: boolean;
  isError: boolean;
}

const ActiveProjectContext = createContext<ActiveProjectValue | null>(null);

function readStored(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(STORAGE_KEY);
}

export function ActiveProjectProvider({ children }: { children: React.ReactNode }) {
  const { data: raw = [], isLoading, isError } = useProjects();
  // El proyecto "default" es un artefacto de backfill pre-multi-tenant — no es un
  // proyecto real del usuario. Lo filtramos para que Studio muestre StudioEntry.
  const projects = raw.filter((p) => p.id !== "default");
  // Arranca desde localStorage (sin tocar window en SSR — null hasta el efecto).
  const [activeId, setActiveIdState] = useState<string | null>(null);

  // Hidratar desde localStorage tras el montaje (SSR-safe).
  useEffect(() => {
    setActiveIdState(readStored());
  }, []);

  const setActiveId = useCallback((id: string) => {
    setActiveIdState(id);
    if (typeof window !== "undefined") window.localStorage.setItem(STORAGE_KEY, id);
  }, []);

  // Reconciliar con la lista cargada: si no hay activo (o el guardado ya no existe),
  // caer al primer proyecto del usuario. Persistimos para futuras sesiones.
  useEffect(() => {
    if (projects.length === 0) return;
    const valid = activeId && projects.some((p) => p.id === activeId);
    if (!valid) setActiveId(projects[0].id);
  }, [projects, activeId, setActiveId]);

  const project = useMemo(
    () => projects.find((p) => p.id === activeId),
    [projects, activeId],
  );

  const value: ActiveProjectValue = {
    project,
    activeId: project?.id ?? null,
    projects,
    setActiveId,
    isLoading,
    isError,
  };

  return (
    <ActiveProjectContext.Provider value={value}>{children}</ActiveProjectContext.Provider>
  );
}

/** Lee el proyecto activo + el switcher. Debe usarse bajo <ActiveProjectProvider>. */
export function useActiveProject(): ActiveProjectValue {
  const ctx = useContext(ActiveProjectContext);
  if (!ctx) {
    throw new Error("useActiveProject debe usarse dentro de <ActiveProjectProvider>");
  }
  return ctx;
}

/** Atajo: solo el id activo (o null). Para pasar a los hooks de listas scopeadas. */
export function useActiveProjectId(): string | null {
  return useActiveProject().activeId;
}
