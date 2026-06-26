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

  // La página de login no lleva chrome ni gate (se renderiza sola).
  if (pathname === "/login") return <>{children}</>;

  // ActiveProjectProvider va dentro del gate (carga GET /projects solo autenticado)
  // y dentro de QueryClientProvider (layout) — useProjects necesita ambos. Scopea
  // toda la consola al proyecto activo (Wave 2).
  return (
    <AuthGate>
      <ActiveProjectProvider>
        <div className="app">
          <Sidebar />
          <main>
            <Topbar />
            {children}
          </main>
        </div>
      </ActiveProjectProvider>
    </AuthGate>
  );
}
