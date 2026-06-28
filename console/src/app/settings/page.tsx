import { SettingsView } from "@/components/settings/SettingsView";
import { ConnectorsSection } from "@/components/settings/ConnectorsSection";
import { AccountSecurity } from "@/components/settings/AccountSecurity";

export default function SettingsPage() {
  return (
    <div className="form-col">
      <SettingsView />
      <ConnectorsSection />
      <AccountSecurity />
    </div>
  );
}
