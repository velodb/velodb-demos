import { Handle, Position, NodeProps } from "@xyflow/react";
import { FileText } from "lucide-react";
import { Badge } from "@/components/ui/badge";

export interface DocumentSourceNodeData {
  corpusName?: string;
  documentCount?: number;
  onClick?: () => void;
}

const DocumentSourceNode = ({ data }: NodeProps<DocumentSourceNodeData>) => {
  const corpusName = data.corpusName || "Documents";
  const documentCount = data.documentCount ?? 0;

  return (
    <div
      className="rounded-lg border-2 border-blue-400 shadow-lg bg-gradient-to-b from-white to-blue-50 relative cursor-pointer hover:shadow-xl transition-shadow"
      style={{ width: 150 }}
      onClick={data.onClick}
      data-testid="document-source-node"
    >
      {/* Header */}
      <div className="px-2 py-1.5 border-b border-blue-200 flex items-center gap-1.5 bg-blue-50/50">
        <FileText className="w-3.5 h-3.5 text-blue-600" />
        <span className="font-semibold text-xs text-blue-800">Documents</span>
      </div>

      {/* Content */}
      <div className="p-2 space-y-1.5">
        <div className="text-[10px] text-gray-600 font-medium truncate" title={corpusName}>
          {corpusName}
        </div>
        <div className="flex items-center justify-between">
          <span className="text-[9px] text-gray-500">Count:</span>
          <Badge variant="secondary" className="h-4 px-1.5 text-[9px] bg-blue-100 text-blue-700 border-blue-200">
            {documentCount.toLocaleString()}
          </Badge>
        </div>
      </div>

      <Handle type="source" position={Position.Right} className="!bg-blue-500" />
    </div>
  );
};

export default DocumentSourceNode;
