import { Database } from "lucide-react";

export const DataLakeNode = () => {
  return (
    <div className="relative bg-gradient-to-br from-[rgba(77,182,172,0.08)] to-[rgba(77,182,172,0.02)] rounded-xl p-6 min-w-[180px] flex flex-col gap-3 border border-[rgba(77,182,172,0.2)] shadow-[0_2px_8px_rgba(77,182,172,0.08)] transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] hover:-translate-y-0.5 hover:shadow-[0_4px_12px_rgba(77,182,172,0.15)] hover:border-[rgba(77,182,172,0.4)]">
      {/* Storage Badge */}
      <div className="absolute top-3 right-3 bg-[rgba(77,182,172,0.1)] text-[#4db6ac] px-2 py-0.5 rounded text-[11px] font-medium">
        STORAGE
      </div>
      
      {/* Icon Container */}
      <div className="flex flex-col items-center gap-3">
        <div className="bg-white/80 rounded-lg p-2 shadow-[0_1px_3px_rgba(0,0,0,0.05)]">
          <Database className="w-10 h-10 text-[#4db6ac]" strokeWidth={2} />
        </div>
        <div className="font-semibold text-lg text-[#4db6ac] tracking-tight text-center">Data Lake</div>
      </div>
    </div>
  );
};
