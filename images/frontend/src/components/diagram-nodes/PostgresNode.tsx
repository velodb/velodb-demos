import postgresLogo from "@/assets/postgresql-logo.png";

interface PostgresNodeProps {
  variant?: "default" | "compact";
  showDetails?: boolean;
}

export const PostgresNode = ({ variant = "default", showDetails = true }: PostgresNodeProps) => {
  return (
    // Main PostgreSQL container - blue gradient
    <div
      className="bg-gradient-to-br from-[rgba(66,133,244,0.08)] to-[rgba(66,133,244,0.02)] rounded-xl p-6 min-w-[180px] flex flex-col gap-3 border border-[rgba(66,133,244,0.2)] shadow-[0_2px_8px_rgba(66,133,244,0.08)] transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] hover:-translate-y-0.5 hover:shadow-[0_4px_12px_rgba(66,133,244,0.15)] hover:border-[rgba(66,133,244,0.4)]"
    >
      {/* Header section */}
      <div className="flex flex-col items-center gap-3">
        <div className="w-12 h-12 flex items-center justify-center">
          <img src={postgresLogo} alt="PostgreSQL Logo" className="w-12 h-12 drop-shadow-[0_2px_4px_rgba(0,0,0,0.1)]" />
        </div>
        <div className="font-semibold text-lg text-[#1a73e8] tracking-tight">PostgreSQL</div>
      </div>
      {/* Details section - inside the yellow container */}
      {showDetails && variant === "default" && (
        <div className="space-y-2 pt-2">
          <div className="bg-gray-700 border-2 border-dashed border-white/40 rounded px-4 py-2 text-white text-sm font-medium text-center">
            WAL
          </div>
          <div className="bg-gray-700 border-2 border-dashed border-white/40 rounded px-4 py-2 text-white text-sm text-center">
            Logical decoding
          </div>
          <div className="bg-gray-700 border-2 border-dashed border-white/40 rounded px-4 py-2 text-white text-sm text-center">
            Replication slot
          </div>
        </div>
      )}
    </div>
  );
};
