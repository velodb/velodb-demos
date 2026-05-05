import { Compass } from "lucide-react";

export const KibanaNode = () => {
  return (
    <div className="relative bg-gradient-to-br from-[rgba(0,85,113,0.08)] to-[rgba(0,85,113,0.02)] rounded-xl p-6 min-w-[180px] flex flex-col gap-3 border border-[rgba(0,85,113,0.2)] shadow-[0_2px_8px_rgba(0,85,113,0.08)] transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] hover:-translate-y-0.5 hover:shadow-[0_4px_12px_rgba(0,85,113,0.15)] hover:border-[rgba(0,85,113,0.4)]">
      {/* Visualization Badge */}
      <div className="absolute top-3 right-3 bg-[rgba(0,169,206,0.1)] text-[#00a9ce] px-2 py-0.5 rounded text-[11px] font-medium">
        VISUALIZATION
      </div>

      {/* Icon Container */}
      <div className="flex flex-col items-center gap-3">
        <div className="bg-white/80 rounded-lg p-2 shadow-[0_1px_3px_rgba(0,0,0,0.05)]">
          <Compass className="w-10 h-10 text-[#005571]" strokeWidth={2} />
        </div>
        <div className="font-semibold text-lg text-[#005571] tracking-tight text-center">Kibana</div>
      </div>
    </div>
  );
};
