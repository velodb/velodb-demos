import { Handle, Position } from '@xyflow/react';
import { ClickstreamNode } from '@/components/diagram-nodes/ClickstreamNode';

export default function WorkflowClickstreamNode() {
  return (
    <div className="relative">
      <ClickstreamNode />
      <Handle
        type="source"
        position={Position.Right}
        className="w-3 h-3 !bg-purple-500 border-2 border-white"
      />
    </div>
  );
}
