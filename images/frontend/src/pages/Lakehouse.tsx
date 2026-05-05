import { Sidebar } from "@/components/Sidebar";
import { Database } from "lucide-react";

const Lakehouse = () => {
  return (
    <div className="flex h-screen bg-background">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-8">
        <div className="mb-6">
          <h1 className="text-3xl font-bold text-foreground mb-2">Lakehouse Architecture</h1>
          <p className="text-muted-foreground">
            Modern data lakehouse patterns with VeloDB
          </p>
        </div>
        
        <div className="flex flex-col items-center justify-center py-16">
          <div className="w-24 h-24 bg-primary/10 rounded-full flex items-center justify-center mb-4">
            <Database className="w-12 h-12 text-primary" />
          </div>
          <p className="text-muted-foreground text-lg">Coming Soon</p>
          <p className="text-muted-foreground text-sm mt-2">
            Lakehouse architecture diagrams will be available here
          </p>
        </div>
      </main>
    </div>
  );
};

export default Lakehouse;
