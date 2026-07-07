"use client";

// AppShell decide el chrome: en /login renderiza SOLO el formulario (pantalla
// completa, sin sidebar/topbar); en el resto envuelve con AuthGate + el chrome
// normal de la consola. Mantiene el RootLayout como server component.

import { usePathname } from "next/navigation";
import { AuthGate } from "./AuthGate";
import { Sidebar } from "./Sidebar";
import { Topbar } from "./Topbar";
import { ActiveProjectProvider } from "@/lib/activeProject";

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  // Páginas de auth/invitación: sin chrome ni sidebar (se renderizan solas). El
  // gate público de estas rutas vive en AuthGate; aquí sólo quitamos el chrome.
  if (pathname === "/login" || pathname === "/register" || pathname.startsWith("/invite/")) {
    return <>{children}</>;
  }

  // ActiveProjectProvider va dentro del gate (carga GET /projects solo autenticado)
  // y dentro de QueryClientProvider (layout) — useProjects necesita ambos. Scopea
  // toda la consola al proyecto activo (Wave 2).
  // Studio es una vista full-workspace calcada del mockup aprobado, que NO lleva la
  // topbar global (su propia studio-topbar es la única barra). En el resto de la
  // consola la topbar global se mantiene.
  const hideTopbar = pathname === "/studio";

  return (
    <AuthGate>
      <ActiveProjectProvider>
        <div className="app">
          <Sidebar />
          <main>
            {!hideTopbar && <Topbar />}
            {children}
          </main>
        </div>
      </ActiveProjectProvider>
    </AuthGate>
  );
}
