import { ArrowRight } from "lucide-react";

interface ArrowProps {
  label?: string;
  vertical?: boolean;
}

export const Arrow = ({ label, vertical = false }: ArrowProps) => {
  if (vertical) {
    return (
      <div className="flex flex-col items-center gap-1">
        <div className="w-0.5 h-12 bg-[#777777]"></div>
        <div className="w-0 h-0 border-l-4 border-l-transparent border-r-4 border-r-transparent border-t-4 border-t-[#777777]"></div>
        {label && (
          <div className="text-xs text-[#777777] mt-1">{label}</div>
        )}
      </div>
    );
  }

  return (
    <div className="flex items-center gap-2">
      <div className="flex items-center">
        <div className="h-0.5 w-12 bg-[#777777]"></div>
        <div className="w-0 h-0 border-t-4 border-t-transparent border-b-4 border-b-transparent border-l-4 border-l-[#777777]"></div>
      </div>
      {label && (
        <div className="text-xs text-[#777777]">{label}</div>
      )}
    </div>
  );
};
