import { Catalogs } from "./Catalogs";

interface UnityCatalogNodeProps {
  showDetails?: boolean;
}

export const UnityCatalogNode = ({
  showDetails = false
}: UnityCatalogNodeProps) => {
  return (
    <div className="flex flex-col gap-2">
      <Catalogs />
      
      {showDetails && (
        <div className="bg-white border border-[#E0E0E0] rounded-lg p-3 shadow-[0_2px_4px_rgba(0,0,0,0.05)]">
          <div className="text-center text-[#777777] font-semibold mb-2 text-sm">
            Governance layer
          </div>
        </div>
      )}
    </div>
  );
};
