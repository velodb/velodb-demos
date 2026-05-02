import velodbLogo from "@/assets/velodb-logo.png";
import { Database } from "lucide-react";

interface VeloDBNodeProps {
  showDetails?: boolean;
}

export const VeloDBNode = ({ showDetails = false }: VeloDBNodeProps) => {
  return (
    <div className="flex flex-col gap-2">
      <div
        className="relative bg-gradient-to-br from-[rgba(99,102,241,0.08)] to-[rgba(99,102,241,0.02)] border-[1.5px] border-[rgba(99,102,241,0.25)] rounded-2xl p-8 px-7 min-w-[240px] shadow-[0_4px_12px_rgba(99,102,241,0.1)] transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] hover:-translate-y-[3px] hover:scale-[1.02] hover:shadow-[0_8px_24px_rgba(99,102,241,0.2),0_0_30px_rgba(99,102,241,0.25)] hover:border-[rgba(99,102,241,0.5)]"
      >
        {/* Subtle background pattern */}
        <div className="absolute inset-0 opacity-[0.02] rounded-2xl" style={{
          backgroundImage: 'repeating-linear-gradient(45deg, transparent, transparent 10px, rgba(0,0,0,0.1) 10px, rgba(0,0,0,0.1) 11px)'
        }}></div>

        <div className="relative flex flex-col items-center gap-4">
          {/* Database icon container */}
          <div className="bg-white/95 rounded-xl p-4 shadow-[0_2px_8px_rgba(99,102,241,0.12)]">
            <Database className="w-[52px] h-[52px] text-[#1a73e8]" strokeWidth={1.5} />
          </div>

          {/* VeloDB logo */}
          <div className="flex items-center justify-center">
            <img src={velodbLogo} alt="VeloDB" className="h-14 object-contain" />
          </div>

          {/* Subtitle */}
          <div className="text-center max-w-[200px]">
            <div className="text-sm text-[rgba(15,23,42,0.6)] font-normal mt-2">
              High-performance analytics engine
            </div>
          </div>

          {/* Status badge */}
          <div className="bg-gradient-to-r from-[#10b981] to-[#059669] text-white px-3 py-1 rounded-md text-[11px] font-semibold shadow-[0_2px_4px_rgba(16,185,129,0.2)]">
            ANALYTICS READY
          </div>
        </div>
      </div>

      {showDetails && (
        <div className="bg-white border border-[#E0E0E0] rounded-lg p-3 shadow-[0_2px_4px_rgba(0,0,0,0.05)]">
          <div className="text-center text-[#777777] font-semibold mb-2 text-sm">
            Change events/rows
          </div>
        </div>
      )}
    </div>
  );
};
