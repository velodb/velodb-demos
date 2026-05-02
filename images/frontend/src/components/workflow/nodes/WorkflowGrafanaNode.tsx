import { Handle, Position } from '@xyflow/react';
import { GrafanaNode } from '@/components/diagram-nodes/GrafanaNode';

export default function WorkflowGrafanaNode() {
  return (
    <div className="relative">
      <GrafanaNode />
      <Handle
        type="target"
        position={Position.Left}
        className="w-3 h-3 !bg-orange-600 border-2 border-white"
      />
    </div>
  );
}
