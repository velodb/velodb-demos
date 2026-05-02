import otelLogo from "@/assets/opentelemetry-logo.png";

export const OpenTelemetryNode = () => {
    return (
        <div className="relative bg-gradient-to-br from-purple-50 to-purple-100/50 border border-purple-200 rounded-xl p-6 min-w-[180px] flex flex-col gap-3 shadow-[0_2px_8px_rgba(168,85,247,0.1)] transition-all duration-300 hover:-translate-y-0.5 hover:shadow-[0_4px_12px_rgba(168,85,247,0.15)]">
            {/* Icon Container */}
            <div className="flex flex-col items-center gap-3">
                <div className="bg-white rounded-lg p-3 shadow-sm border border-purple-100">
                    <img src={otelLogo} alt="OpenTelemetry" className="w-8 h-8 object-contain" />
                </div>
                <div className="font-semibold text-lg text-purple-900 tracking-tight text-center">OpenTelemetry</div>
            </div>
        </div>
    );
};
