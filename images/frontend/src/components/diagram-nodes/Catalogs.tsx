import icebergLogo from "@/assets/apache-iceberg-logo.png";
import awsGlueLogo from "@/assets/aws-glue-logo.svg";

interface CatalogsProps {
  variant?: "default" | "compact";
}

export const Catalogs = ({ variant = "default" }: CatalogsProps) => {
  const isCompact = variant === "compact";

  if (isCompact) {
    return (
      <div className="bg-gradient-to-br from-purple-50/80 to-indigo-50/60 rounded-2xl shadow-lg border border-purple-100/50 px-6 py-5 w-[360px]">
        <div className="text-center mb-4">
          <h3 className="text-xl font-semibold text-slate-800 mb-1 tracking-tight">Data Catalogs</h3>
          <p className="text-xs text-slate-600/80">Unity Catalog, Iceberg, AWS Glue</p>
        </div>

        <div className="grid grid-cols-3 gap-6">
          {/* Unity Catalog */}
          <div className="group relative flex flex-col items-center">
            <div className="bg-white rounded-lg shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-purple-300/70 cursor-pointer w-full aspect-square flex items-center justify-center p-3">
              <div className="relative flex items-center justify-center w-8 h-8 overflow-hidden">
                <img
                  src="https://raw.githubusercontent.com/unitycatalog/unitycatalog/main/ui/public/uc-logo-reverse.png"
                  alt="Unity Catalog"
                  className="w-full h-full object-contain scale-[3] translate-x-[33px]"
                />
              </div>
            </div>
            <p className="mt-2 text-[10px] font-semibold text-slate-700 text-center">Unity Catalog</p>
          </div>

          {/* Apache Iceberg */}
          <div className="group relative flex flex-col items-center">
            <div className="bg-white rounded-lg shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-purple-300/70 cursor-pointer w-full aspect-square flex items-center justify-center p-3">
              <div className="relative flex items-center justify-center w-8 h-8">
                <img src={icebergLogo} alt="Apache Iceberg" className="w-full h-full object-contain" />
              </div>
            </div>
            <p className="mt-2 text-[10px] font-semibold text-slate-700 text-center">Apache Iceberg</p>
          </div>

          {/* AWS Glue */}
          <div className="group relative flex flex-col items-center">
            <div className="bg-white rounded-lg shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-purple-300/70 cursor-pointer w-full aspect-square flex items-center justify-center p-3">
              <div className="relative flex items-center justify-center w-8 h-8">
                <img src={awsGlueLogo} alt="AWS Glue" className="w-full h-full object-contain" />
              </div>
            </div>
            <p className="mt-2 text-[10px] font-semibold text-slate-700 text-center">AWS Glue</p>
          </div>
        </div>
      </div>
    );
  }

  return <div className={`bg-gradient-to-br from-purple-50/80 to-indigo-50/60 rounded-2xl shadow-lg border border-purple-100/50 ${isCompact ? "px-6 py-5 w-[360px]" : "px-12 py-14"}`}>
    {/* Section Header */}
    <div className={`text-center ${isCompact ? "mb-4" : "mb-12"}`}>
      <h3 className={`${isCompact ? "text-xl" : "text-2xl"} font-semibold text-slate-800 mb-2 tracking-tight`}>Data Catalogs</h3>
      <p className={`${isCompact ? "text-xs" : "text-sm"} text-slate-600/80`}>Connect Data Without Vendor Lock Concerns                     </p>
    </div>

    {/* Grid Layout */}
    <div className={`grid grid-cols-1 md:grid-cols-3 ${isCompact ? "gap-6" : "max-w-5xl mx-auto gap-10"}`}>
      {/* Unity Catalog */}
      <div className="group relative flex flex-col items-center">
        <div className="bg-gradient-to-b from-white to-slate-50/50 rounded-lg shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-purple-300/70 cursor-pointer w-full aspect-[3/1] flex items-center justify-center p-4">
          <div className="relative flex items-center justify-center w-10 h-10 overflow-hidden">
            <img
              src="https://raw.githubusercontent.com/unitycatalog/unitycatalog/main/ui/public/uc-logo-reverse.png"
              alt="Unity Catalog"
              className="w-full h-full object-contain scale-[3] translate-x-[33px]"
            />
          </div>
        </div>
        <p className="mt-2 text-xs font-semibold text-slate-700 text-center">Unity Catalog</p>
      </div>

      {/* Apache Iceberg */}
      <div className="group relative flex flex-col items-center">
        <div className="bg-gradient-to-b from-white to-slate-50/50 rounded-lg p-4 shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-purple-300/70 cursor-pointer w-full aspect-[3/1] flex items-center justify-center">
          <div className="relative flex items-center justify-center w-10 h-10">
            <img src={icebergLogo} alt="Apache Iceberg" className="w-full h-full object-contain" />
          </div>
        </div>
        <p className="mt-2 text-xs font-semibold text-slate-700 text-center">Apache Iceberg</p>
      </div>

      {/* AWS Glue */}
      <div className="group relative flex flex-col items-center">
        <div className="bg-gradient-to-b from-white to-slate-50/50 rounded-lg p-4 shadow-sm border border-slate-200/60 transition-all duration-300 ease-out hover:shadow-md hover:-translate-y-1 hover:scale-[1.02] hover:border-purple-300/70 cursor-pointer w-full aspect-[3/1] flex items-center justify-center">
          <div className="relative flex items-center justify-center w-10 h-10">
            <img src={awsGlueLogo} alt="AWS Glue" className="w-full h-full object-contain" />
          </div>
        </div>
        <p className="mt-2 text-xs font-semibold text-slate-700 text-center">AWS Glue</p>
      </div>
    </div>
  </div>;
};
