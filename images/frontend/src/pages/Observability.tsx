import { Sidebar } from "@/components/Sidebar";
import { ObservabilityDiagramDetailed } from "@/components/diagrams/ObservabilityDiagram";

const Observability = () => {
  return (
    <div className="flex h-screen bg-white">
      <Sidebar />
      
      <main className="flex-1 overflow-y-auto">
        <div className="p-8">
          <div className="mb-8">
            <h1 className="text-3xl font-bold text-[#333333] mb-2">Observability Architecture</h1>
            <p className="text-[#777777]">
              Real-time data pipeline for monitoring and observability workloads with PostgreSQL CDC
            </p>
          </div>

          <ObservabilityDiagramDetailed />
        </div>
      </main>
    </div>
  );
};

export default Observability;
