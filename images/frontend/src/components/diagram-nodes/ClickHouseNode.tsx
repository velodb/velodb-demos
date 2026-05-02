interface ClickHouseNodeProps {
  variant?: "sink" | "target";
}

export const ClickHouseNode = ({ variant = "sink" }: ClickHouseNodeProps) => {
  if (variant === "sink") {
    return (
      <div className="bg-[#DFFF00] rounded-lg p-4 min-w-[160px]">
        <div className="space-y-2">
          <div className="font-bold text-lg text-black text-center">
            ClickHouse
          </div>
          <div className="font-semibold text-black text-center">
            Connect Sink
          </div>
          <div className="text-center text-sm text-black/70">or</div>
          <div className="font-semibold text-black text-center">
            HTTP Sink
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="bg-[#DFFF00] rounded-lg p-4 min-w-[140px]">
      <div className="font-bold text-lg text-black text-center">ClickHouse</div>
    </div>
  );
};
