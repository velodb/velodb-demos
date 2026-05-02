import { Handle, Position, NodeProps } from "@xyflow/react";
import { Scissors, Loader2, Binary } from "lucide-react";
import { Badge } from "@/components/ui/badge";

export interface ChunkProcessorNodeData {
  chunkCount?: number;
  vectorCount?: number;
  embeddingDimension?: number;
  isProcessing?: boolean;
  onClick?: () => void;
}

const ChunkProcessorNode = ({ data }: NodeProps<ChunkProcessorNodeData>) => {
  const chunkCount = data.chunkCount ?? 0;
  const vectorCount = data.vectorCount ?? 0;
  const embeddingDimension = data.embeddingDimension ?? 1024;
  const isProcessing = data.isProcessing ?? false;

  return (
    <div
      className="rounded-lg border-2 border-purple-400 shadow-lg bg-gradient-to-b from-white to-purple-50 relative cursor-pointer hover:shadow-xl transition-shadow"
      style={{ width: 150 }}
      onClick={data.onClick}
      data-testid="chunk-processor-node"
    >
      <Handle type="target" position={Position.Left} className="!bg-purple-500" />

      {/* Header */}
      <div className="px-2 py-1.5 border-b border-purple-200 flex items-center gap-1.5 bg-purple-50/50">
        <Scissors className="w-3.5 h-3.5 text-purple-600" />
        <span className="font-semibold text-xs text-purple-800">Chunks</span>
      </div>

      {/* Content */}
      <div className="p-2 space-y-1.5">
        {isProcessing ? (
          <div className="flex items-center gap-1.5 justify-center py-1">
            <Loader2 className="w-3 h-3 text-purple-600 animate-spin" />
            <span className="text-[10px] text-purple-600 font-medium">Processing...</span>
          </div>
        ) : (
          <>
            <div className="text-[10px] text-gray-600 font-medium">Ready</div>
            <div className="flex items-center justify-between">
              <span className="text-[9px] text-gray-500">Chunks:</span>
              <Badge variant="secondary" className="h-4 px-1.5 text-[9px] bg-purple-100 text-purple-700 border-purple-200">
                {chunkCount.toLocaleString()}
              </Badge>
            </div>
            {vectorCount > 0 && (
              <div className="flex items-center justify-between mt-0.5">
                <span className="text-[9px] text-gray-500 flex items-center gap-0.5">
                  <Binary className="w-2.5 h-2.5" />
                  Vectors:
                </span>
                <Badge variant="secondary" className="h-4 px-1.5 text-[9px] bg-cyan-100 text-cyan-700 border-cyan-200">
                  {vectorCount.toLocaleString()} x {embeddingDimension}
                </Badge>
              </div>
            )}
          </>
        )}
      </div>

      <Handle type="source" position={Position.Right} className="!bg-purple-500" />
    </div>
  );
};

export default ChunkProcessorNode;
