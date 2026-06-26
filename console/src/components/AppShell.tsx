"use client";

// AppShell decide el chrome: en /login renderiza SOLO el formulario (pantalla
// completa, sin sidebar/topbar); en el resto envuelve con AuthGate + el chrome
// normal de la consola. Mantiene el RootLayout como server component.

import { usePathname } from "next/navigation";
import { AuthGate } from "./AuthGate";
import { Sidebar } from "./Sidebar";
import { Topbar } from "./Topbar";

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  // La página de login no lleva chrome ni gate (se renderiza sola).
  if (pathname === "/login") return <>{children}</>;

  return (
    <AuthGate>
      <div className="app">
        <Sidebar />
        <main>
          <Topbar />
          {children}
        </main>
      </div>
    </AuthGate>
  );
}
