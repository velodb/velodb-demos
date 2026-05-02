interface KafkaNodeProps {
  variant?: "default" | "large";
}

export const KafkaNode = ({ variant = "default" }: KafkaNodeProps) => {
  return (
    <div className="relative bg-gradient-to-br from-[rgba(0,0,0,0.04)] to-[rgba(0,0,0,0.01)] border border-[rgba(0,0,0,0.12)] border-l-[3px] border-l-[#231F20] rounded-xl p-7 px-6 min-w-[200px] min-h-[180px] shadow-[0_2px_8px_rgba(0,0,0,0.04)] transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] hover:-translate-y-0.5 hover:shadow-[0_4px_16px_rgba(0,0,0,0.1)] hover:border-[rgba(0,0,0,0.2)]">
      {/* Pulse indicator - active streaming */}
      <div className="absolute top-3 right-3 w-2 h-2 bg-[#10b981] rounded-full animate-pulse"></div>

      <div className="flex flex-col items-center gap-3.5">
        {/* Icon container */}
        <div className="bg-white/90 rounded-[10px] p-3 shadow-[0_2px_6px_rgba(0,0,0,0.08)] transition-colors hover:bg-white">
          <div className="grid grid-cols-2 gap-1">
            <div className="w-3 h-3 bg-[#231F20] rounded-full"></div>
            <div className="w-3 h-3 bg-[#231F20] rounded-full"></div>
            <div className="w-3 h-3 bg-[#231F20] rounded-full"></div>
            <div className="w-3 h-3 bg-[#231F20] rounded-full"></div>
          </div>
        </div>

        {/* Text content */}
        <div className="text-center">
          <div className="font-semibold text-lg text-[#231F20] tracking-tight">Kafka</div>
          <div className="text-[13px] text-black/50 mt-1 font-normal">Event streaming backbone</div>
        </div>
      </div>
    </div>
  );
};
