import { SettingsView } from "@/components/settings/SettingsView";

// SettingsView compone las 4 secciones por alcance (Conexiones · Proyecto ·
// Cuenta · Avanzado/Legacy), incluidas Notificaciones (ConnectorsSection) y
// Cuenta y seguridad (AccountSecurity), en orden.
export default function SettingsPage() {
  return (
    <div className="form-col">
      <SettingsView />
    </div>
  );
}
