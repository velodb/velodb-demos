interface DebeziumNodeProps {
  showKafkaConnect?: boolean;
}

export const DebeziumNode = ({ showKafkaConnect = true }: DebeziumNodeProps) => {
  return (
    <div className="flex flex-col gap-2">
      <div className="bg-[#EDF7FF] border border-[#BDE0FF] rounded-lg p-4 min-w-[140px]">
        <div className="flex items-center gap-2 justify-center">
          <div className="w-8 h-8 bg-[#007BFF]/10 rounded flex items-center justify-center">
            <div className="grid grid-cols-2 gap-0.5">
              <div className="w-2 h-2 bg-[#007BFF] rounded-sm"></div>
              <div className="w-2 h-2 bg-[#007BFF] rounded-sm"></div>
              <div className="w-2 h-2 bg-[#007BFF] rounded-sm"></div>
              <div className="w-2 h-2 bg-[#007BFF] rounded-sm"></div>
            </div>
          </div>
          <div className="font-bold text-[#007BFF] text-lg">Debezium</div>
        </div>
      </div>
      
      {showKafkaConnect && (
        <div className="bg-white border border-[#E0E0E0] rounded-lg p-3 text-center shadow-[0_2px_4px_rgba(0,0,0,0.05)]">
          <div className="flex items-center gap-2 justify-center">
            <div className="grid grid-cols-2 gap-0.5">
              <div className="w-1.5 h-1.5 bg-[#333333] rounded-sm"></div>
              <div className="w-1.5 h-1.5 bg-[#333333] rounded-sm"></div>
              <div className="w-1.5 h-1.5 bg-[#333333] rounded-sm"></div>
              <div className="w-1.5 h-1.5 bg-[#333333] rounded-sm"></div>
            </div>
            <span className="text-[#333333] text-sm font-medium">Kafka Connect</span>
          </div>
        </div>
      )}
    </div>
  );
};
