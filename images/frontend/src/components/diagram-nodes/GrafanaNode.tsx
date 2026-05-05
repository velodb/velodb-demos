import { BarChart3 } from "lucide-react";

interface GrafanaNodeProps {
  onClick?: () => void;
}

export const GrafanaNode = ({ onClick }: GrafanaNodeProps) => {
  return (
    <div
      onClick={onClick}
      className="relative bg-gradient-to-br from-[rgba(244,104,0,0.08)] to-[rgba(244,104,0,0.02)] rounded-xl p-6 min-w-[180px] flex flex-col gap-3 border border-[rgba(244,104,0,0.2)] shadow-[0_2px_8px_rgba(244,104,0,0.08)] transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] hover:-translate-y-0.5 hover:shadow-[0_4px_12px_rgba(244,104,0,0.15)] hover:border-[rgba(244,104,0,0.4)] cursor-pointer"
    >
      {/* Metrics Badge */}
      <div className="absolute top-3 right-3 bg-[rgba(244,104,0,0.1)] text-[#f46800] px-2 py-0.5 rounded text-[11px] font-medium">
        METRICS
      </div>

      {/* Icon Container */}
      <div className="flex flex-col items-center gap-3">
        <div className="bg-white/80 rounded-lg p-2 shadow-[0_1px_3px_rgba(0,0,0,0.05)]">
          <BarChart3 className="w-10 h-10 text-[#f46800]" strokeWidth={2} />
        </div>
        <div className="font-semibold text-lg text-[#f46800] tracking-tight text-center">Grafana</div>
      </div>
    </div>
  );
};
