import { Sidebar } from "@/components/Sidebar";

const Query = () => {
  return (
    <div className="flex h-screen bg-background">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-8">
        <h1 className="text-3xl font-bold text-foreground mb-4">Query</h1>
        <p className="text-muted-foreground">Execute SQL queries and analyze data</p>
      </main>
    </div>
  );
};

export default Query;
