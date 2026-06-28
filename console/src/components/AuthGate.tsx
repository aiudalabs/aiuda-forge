"use client";

// AuthGate (audit C1): cuando el control-plane exige auth y no hay token, redirige
// a /login antes de renderizar la consola. En modo mock / loopback abierto (la API
// no responde con 401), deja pasar sin login. La propia /login NO se envuelve aquí
// (ver layout) para no crear un bucle.

import { useEffect, useState } from "react";
import { authRequired, getToken } from "@/lib/auth";

export function AuthGate({ children }: { children: React.ReactNode }) {
  // ready=false → estamos comprobando; no renderizamos la app todavía para evitar
  // un flash de contenido protegido antes de redirigir.
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      // Rutas públicas que no se gatean: /login y /register (evitan el bucle de
      // redirección) y /invite/* (su página maneja su propio flujo de sesión).
      if (typeof window !== "undefined") {
        const p = window.location.pathname;
        if (p === "/login" || p === "/register" || p.startsWith("/invite/")) {
          if (!cancelled) setReady(true);
          return;
        }
      }
      // Con token ya guardado, dejamos pasar (un 401 posterior lo maneja lib/api).
      if (getToken()) {
        if (!cancelled) setReady(true);
        return;
      }
      // Sin token: ¿la API exige auth? Si sí, a /login. Si no (mock/abierto), pasa.
      const required = await authRequired();
      if (cancelled) return;
      if (required) {
        const next = encodeURIComponent(window.location.pathname + window.location.search);
        window.location.href = `/login?next=${next}`;
        return;
      }
      setReady(true);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  if (!ready) return null;
  return <>{children}</>;
}
