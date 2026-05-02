import { Sidebar } from "@/components/Sidebar";
import WorkflowCanvas from '@/components/workflow/WorkflowCanvas';

const Index = () => {
  return (
    <div className="flex h-screen bg-white">
      <Sidebar />

      <main className="flex-1 overflow-hidden">
        <div className="h-full flex flex-col">
          <div className="px-8 py-6 bg-white border-b border-gray-200">
            <h1 className="text-3xl font-bold text-[#333333] mb-2">Customer Facing Architecture</h1>
            <p className="text-[#777777]">
              Interactive data pipeline from sources to visualization - Postgres CDC → Flink → Kafka → VeloDB → Dashboards
            </p>
          </div>

          <div className="flex-1">
            <WorkflowCanvas />
          </div>
        </div>
      </main>
    </div>
  );
};

export default Index;
