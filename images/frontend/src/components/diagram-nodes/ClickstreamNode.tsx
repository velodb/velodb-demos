import { Activity } from "lucide-react";

export const ClickstreamNode = () => {
  return (
    <div
      className="relative bg-gradient-to-br from-[rgba(168,85,247,0.06)] to-[rgba(168,85,247,0.02)] rounded-xl p-6 min-w-[180px] flex flex-col gap-3 border border-[rgba(168,85,247,0.15)] shadow-[0_2px_8px_rgba(168,85,247,0.06)] transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] hover:-translate-y-0.5 hover:shadow-[0_4px_12px_rgba(168,85,247,0.12)] hover:border-[rgba(168,85,247,0.3)] hover:bg-gradient-to-br hover:from-[rgba(168,85,247,0.08)] hover:to-[rgba(168,85,247,0.04)]"
    >
      {/* Real-time Badge */}
      <div className="absolute top-3 right-3 bg-[rgba(16,185,129,0.1)] text-[#10b981] px-2 py-0.5 rounded text-[11px] font-medium">
        REAL-TIME
      </div>

      {/* Icon Container */}
      <div className="flex flex-col items-center gap-3">
        <div className="bg-white/80 rounded-lg p-2 shadow-[0_1px_3px_rgba(0,0,0,0.05)]">
          <Activity className="w-10 h-10 text-[#a855f7]" strokeWidth={2} />
        </div>
        <div className="font-semibold text-lg text-[#0f172a] tracking-tight text-center">Clickstream</div>
      </div>
    </div>
  );
};
