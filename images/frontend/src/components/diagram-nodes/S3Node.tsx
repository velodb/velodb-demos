import { Database } from "lucide-react";

export const S3Node = () => {
  return (
    <div className="bg-white border border-[#E0E0E0] rounded-lg p-4 min-w-[160px] shadow-[0_2px_4px_rgba(0,0,0,0.05)]">
      <div className="flex items-center gap-2 justify-center">
        <Database className="w-6 h-6 text-orange-500" />
        <div className="font-bold text-lg text-[#333333]">S3 Lakehouse</div>
      </div>
    </div>
  );
};
