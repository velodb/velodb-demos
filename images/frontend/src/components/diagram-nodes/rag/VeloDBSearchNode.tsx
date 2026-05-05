import { Handle, Position, NodeProps } from "@xyflow/react";
import { Database, Search, Layers, GitBranch } from "lucide-react";
import { Button } from "@/components/ui/button";

export type VeloDBPanelType = "hybrid" | "context" | "graph";

export interface VeloDBSearchNodeData {
  onPanelSelect?: (panel: VeloDBPanelType) => void;
}

const VeloDBSearchNode = ({ data }: NodeProps<{ onPanelSelect?: (panel: VeloDBPanelType) => void }>) => {
  const handleButtonClick = (panel: VeloDBPanelType, e: React.MouseEvent) => {
    e.stopPropagation(); // Prevent node click from firing
    data.onPanelSelect?.(panel);
  };

  return (
    <div
      className="rounded-lg border-2 border-cyan-400 shadow-lg bg-gradient-to-b from-white to-cyan-50 relative"
      style={{ width: 150 }}
      data-testid="velodb-search-node"
    >
      <Handle type="target" position={Position.Left} className="!bg-cyan-500" />

      {/* Header */}
      <div className="px-2 py-1.5 border-b border-cyan-200 flex items-center gap-1.5 bg-cyan-50/50">
        <Database className="w-3.5 h-3.5 text-cyan-600" />
        <span className="font-semibold text-xs text-cyan-800">VeloDB</span>
      </div>

      {/* Capability Buttons */}
      <div className="p-1.5 space-y-1">
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start gap-1.5 h-6 text-[10px] font-medium hover:bg-cyan-100 hover:text-cyan-700 px-1.5"
          onClick={(e) => handleButtonClick("hybrid", e)}
          data-testid="btn-hybrid-search"
        >
          <Search className="w-2.5 h-2.5 flex-shrink-0" />
          Hybrid Search
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start gap-1.5 h-6 text-[10px] font-medium hover:bg-cyan-100 hover:text-cyan-700 px-1.5"
          onClick={(e) => handleButtonClick("context", e)}
          data-testid="btn-context-engineering"
        >
          <Layers className="w-2.5 h-2.5 flex-shrink-0" />
          Context Eng.
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start gap-1.5 h-6 text-[10px] font-medium hover:bg-cyan-100 hover:text-cyan-700 px-1.5"
          onClick={(e) => handleButtonClick("graph", e)}
          data-testid="btn-knowledge-graph"
        >
          <GitBranch className="w-2.5 h-2.5 flex-shrink-0" />
          Knowledge Graph
        </Button>
      </div>

      <Handle type="source" position={Position.Right} className="!bg-cyan-500" />
    </div>
  );
};

export default VeloDBSearchNode;
