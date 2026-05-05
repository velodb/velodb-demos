import { useCallback } from "react";
import { ApplicationNode } from "../diagram-nodes/ApplicationNode";
import { OpenTelemetryNode } from "../diagram-nodes/OpenTelemetryNode";
import { VeloDBNode } from "../diagram-nodes/VeloDBNode";
import { GrafanaNode } from "../diagram-nodes/GrafanaNode";

// Helper to get dynamic URL - uses current hostname if env var is empty or contains localhost
const getDynamicUrl = (envUrl: string | undefined, port: number, path: string = "") => {
  if (!envUrl || envUrl.includes("localhost")) {
    return `${window.location.protocol}//${window.location.hostname}:${port}${path}`;
  }
  return envUrl;
};

const Connection = ({ label, color = "#a855f7" }: { label: string; color?: string }) => {
  return (
    <div className="flex flex-col items-center justify-center min-w-[120px] relative px-0">
      <div className="relative w-full flex items-center">
        {/* Left Dot */}
        <div className="w-2.5 h-2.5 rounded-full bg-white border-[2.5px] absolute -left-[5px] z-10" style={{ borderColor: color }} />

        {/* Line */}
        <div className="w-full h-[2px] border-t-2 border-dashed" style={{ borderColor: color }} />

        {/* Right Dot */}
        <div className="w-2.5 h-2.5 rounded-full bg-white border-[2.5px] absolute -right-[5px] z-10" style={{ borderColor: color }} />

        {/* Label */}
        <div className="absolute -top-6 left-1/2 -translate-x-1/2 text-xs font-medium text-gray-500 whitespace-nowrap bg-white px-2">
          {label}
        </div>
      </div>
    </div>
  );
};

export const ObservabilityDiagramDetailed = () => {
  const handleGrafanaClick = useCallback(() => {
    window.open(getDynamicUrl(import.meta.env.VITE_GRAFANA_URL, 33000), "_blank");
  }, []);

  const handleApplicationClick = useCallback(() => {
    window.open(getDynamicUrl(import.meta.env.VITE_OTEL_FRONTEND_URL, 8080), "_blank");
  }, []);

  const handleFeatureFlagClick = useCallback(() => {
    window.open(getDynamicUrl(import.meta.env.VITE_OTEL_FEATURE_FLAG_URL, 8080, "/feature"), "_blank");
  }, []);

  return (
    <div className="bg-white rounded-lg p-12 overflow-x-auto">
      <div className="flex items-center min-w-max justify-center">
        {/* Application Component */}
        <ApplicationNode
          onClick={handleApplicationClick}
          onFeatureFlagClick={handleFeatureFlagClick}
        />

        {/* Connection: App -> OTel */}
        <Connection label="Logs/Traces/Metrics" color="#9333ea" />

        {/* OpenTelemetry */}
        <OpenTelemetryNode />

        {/* Connection: OTel -> VeloDB */}
        <Connection label="Ingest" color="#db2777" />

        {/* VeloDB */}
        <VeloDBNode />

        {/* Connection: VeloDB -> Grafana */}
        <Connection label="Query/Visualize" color="#f97316" />

        {/* Grafana */}
        <GrafanaNode onClick={handleGrafanaClick} />
      </div>
    </div>
  );
};
