import { Monitor, Server, Database, FileCode, Coffee, Flag } from "lucide-react";
import postgresLogo from "@/assets/postgresql-logo.png";

interface ApplicationNodeProps {
  onClick?: () => void;
  onFeatureFlagClick?: () => void;
}

export const ApplicationNode = ({ onClick, onFeatureFlagClick }: ApplicationNodeProps) => {
    return (
        <div
            onClick={onClick}
            className="relative bg-[#F8F9FA] border border-[#E0E0E0] rounded-2xl p-6 flex flex-col gap-6 shadow-sm min-w-[280px] transition-all duration-300 hover:shadow-md cursor-pointer"
        >
            <div className="absolute top-0 left-0 w-full h-1 bg-gradient-to-r from-blue-400 to-blue-600 rounded-t-2xl" />

            {/* Feature Flag CTA Button - Top Right */}
            <button
                onClick={(e) => {
                    e.stopPropagation();
                    onFeatureFlagClick?.();
                }}
                className="absolute top-3 right-3 bg-blue-500 hover:bg-blue-600 text-white p-2 rounded-lg transition-all duration-200 shadow-sm hover:shadow-md z-20"
                title="Feature Flags"
            >
                <Flag className="w-4 h-4" />
            </button>

            <div className="text-lg font-semibold text-[#333333] mb-2">Application Component</div>

            <div className="flex flex-col gap-6 relative">
                {/* Connecting Line */}
                <div className="absolute left-[26px] top-10 bottom-10 w-[2px] bg-blue-200/50 -z-10" />

                {/* Frontend */}
                <div className="flex items-center gap-4 bg-white p-3 rounded-xl border border-blue-100 shadow-sm z-10">
                    <div className="w-12 h-12 bg-blue-50 rounded-lg flex items-center justify-center border border-blue-100">
                        <FileCode className="w-7 h-7 text-blue-600" />
                    </div>
                    <div>
                        <div className="font-semibold text-gray-800">Frontend</div>
                        <div className="text-xs text-gray-500">TypeScript</div>
                    </div>
                </div>

                {/* Backend */}
                <div className="flex items-center gap-4 bg-white p-3 rounded-xl border border-blue-100 shadow-sm z-10">
                    <div className="w-12 h-12 bg-blue-50 rounded-lg flex items-center justify-center border border-blue-100">
                        <div className="flex gap-0.5">
                            <Coffee className="w-4 h-4 text-blue-600" />
                            <span className="text-blue-600 font-bold text-xs">C++</span>
                        </div>
                    </div>
                    <div>
                        <div className="font-semibold text-gray-800">Backend</div>
                        <div className="text-xs text-gray-500">Java/C++</div>
                    </div>
                </div>

                {/* Database */}
                <div className="flex items-center gap-4 bg-white p-3 rounded-xl border border-blue-100 shadow-sm z-10">
                    <div className="w-12 h-12 bg-blue-50 rounded-lg flex items-center justify-center border border-blue-100">
                        <img src={postgresLogo} alt="Postgres" className="w-8 h-8 object-contain" />
                    </div>
                    <div>
                        <div className="font-semibold text-gray-800">Database</div>
                        <div className="text-xs text-gray-500">MySQL/Postgres</div>
                    </div>
                </div>
            </div>
        </div>
    );
};
