import { Database, Cloud, Server } from "lucide-react";

interface ObjectStorageNodeProps {
  variant?: "default" | "compact";
}

export const ObjectStorageNode = ({ variant = "default" }: ObjectStorageNodeProps) => {
  const isCompact = variant === "compact";

  return (
    <div className={`bg-gradient-to-br from-orange-50/80 to-blue-50/60 rounded-2xl shadow-lg border border-orange-100/50 ${isCompact ? "px-6 py-5 w-[360px]" : "px-12 py-14"}`}>
      {/* Section Header */}
      <div className={`text-center ${isCompact ? "mb-4" : "mb-10"}`}>
        <h3 className="text-xl font-semibold text-slate-800 mb-1 tracking-tight">Object Storage</h3>
        <p className="text-xs text-slate-600/80">Unified Data Lake Access</p>
      </div>

      {/* Grid Layout */}
      <div className={`grid grid-cols-1 md:grid-cols-3 gap-6`}>
        {/* AWS S3 */}
        <div className="group relative flex flex-col items-center">
          <div className="bg-white rounded-lg shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-orange-300/70 cursor-pointer w-full aspect-square flex items-center justify-center p-3">
            <Database className="w-8 h-8 text-[#FF9900]" />
          </div>
          <p className="mt-2 text-[10px] font-semibold text-slate-700 text-center">AWS S3</p>
        </div>

        {/* GCP GCS */}
        <div className="group relative flex flex-col items-center">
          <div className="bg-white rounded-lg shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-blue-300/70 cursor-pointer w-full aspect-square flex items-center justify-center p-3">
            <Cloud className="w-8 h-8 text-[#4285F4]" />
          </div>
          <p className="mt-2 text-[10px] font-semibold text-slate-700 text-center">GCP GCS</p>
        </div>

        {/* Azure Blob */}
        <div className="group relative flex flex-col items-center">
          <div className="bg-white rounded-lg shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-blue-500/70 cursor-pointer w-full aspect-square flex items-center justify-center p-3">
            <Server className="w-8 h-8 text-[#0078D4]" />
          </div>
          <p className="mt-2 text-[10px] font-semibold text-slate-700 text-center">Azure Blob</p>
        </div>
      </div>
    </div>
  );
};
