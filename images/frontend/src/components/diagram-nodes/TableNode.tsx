interface TableNodeProps {
  title: string;
  type?: "receiving" | "materialized";
  details?: string[];
}

export const TableNode = ({ title, type = "receiving", details }: TableNodeProps) => {
  return (
    <div className="bg-white border border-[#E0E0E0] rounded-lg p-4 min-w-[160px] shadow-[0_2px_4px_rgba(0,0,0,0.05)]">
      <div className="space-y-2">
        <div className="text-center">
          <div className="font-bold text-[#333333]">{title}</div>
          {type === "materialized" && (
            <div className="text-xs text-[#777777] mt-1">Materialized View</div>
          )}
        </div>
        
        {details && details.length > 0 && (
          <div className="space-y-1 mt-3">
            {details.map((detail, i) => (
              <div key={i} className="bg-gray-100 rounded px-2 py-1 text-xs text-accent">
                {detail}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};
