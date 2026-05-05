import { Handle, Position } from '@xyflow/react';
import { FlinkNode } from '@/components/diagram-nodes/FlinkNode';

export default function WorkflowFlinkNode() {
  return (
    <div className="relative">
      <FlinkNode />
      <Handle
        type="target"
        position={Position.Left}
        className="w-3 h-3 !bg-pink-500 border-2 border-white"
      />
      <Handle
        type="source"
        position={Position.Right}
        className="w-3 h-3 !bg-pink-500 border-2 border-white"
      />
    </div>
  );
}
