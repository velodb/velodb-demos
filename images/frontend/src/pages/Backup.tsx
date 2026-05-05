import { Sidebar } from "@/components/Sidebar";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";

const Backup = () => {
  return (
    <div className="flex h-screen bg-background">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-8">
        <div className="mb-6">
          <h1 className="text-3xl font-bold text-foreground mb-2">Backup</h1>
          <p className="text-muted-foreground">Manage backup plans and restore data</p>
        </div>
        
        <div className="flex flex-col items-center justify-center py-16">
          <div className="w-24 h-24 bg-primary/10 rounded-full flex items-center justify-center mb-4">
            <Archive className="w-12 h-12 text-primary" />
          </div>
          <p className="text-muted-foreground mb-6">There is no backup for this warehouse</p>
          <Button className="gap-2">
            <Plus className="w-4 h-4" />
            Create Backup Plan
          </Button>
        </div>
      </main>
    </div>
  );
};

const Archive = ({ className }: { className?: string }) => (
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width="24"
    height="24"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    className={className}
  >
    <rect width="20" height="5" x="2" y="3" rx="1" />
    <path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8" />
    <path d="M10 12h4" />
  </svg>
);

export default Backup;
