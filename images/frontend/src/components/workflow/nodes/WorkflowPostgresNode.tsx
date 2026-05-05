import { Handle, Position } from '@xyflow/react';
import { PostgresNode } from '@/components/diagram-nodes/PostgresNode';

export default function WorkflowPostgresNode() {
  return (
    <div className="relative">
      <PostgresNode variant="compact" showDetails={false} />
      <Handle
        type="source"
        position={Position.Right}
        className="w-3 h-3 !bg-blue-500 border-2 border-white"
      />
    </div>
  );
}
