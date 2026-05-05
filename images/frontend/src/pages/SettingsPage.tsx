import { Sidebar } from "@/components/Sidebar";

const SettingsPage = () => {
  return (
    <div className="flex h-screen bg-background">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-8">
        <h1 className="text-3xl font-bold text-foreground mb-4">Settings</h1>
        <p className="text-muted-foreground">Configure your VeloDB instance</p>
      </main>
    </div>
  );
};

export default SettingsPage;
