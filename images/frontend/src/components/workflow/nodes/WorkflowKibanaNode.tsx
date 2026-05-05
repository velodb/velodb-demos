import { Handle, Position } from '@xyflow/react';
import { KibanaNode } from '@/components/diagram-nodes/KibanaNode';

export default function WorkflowKibanaNode() {
  return (
    <div className="relative">
      <KibanaNode />
      <Handle
        type="target"
        position={Position.Left}
        className="w-3 h-3 !bg-teal-600 border-2 border-white"
      />
    </div>
  );
}
