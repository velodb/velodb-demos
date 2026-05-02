import { Handle, Position } from '@xyflow/react';
import { ObjectStorageNode } from '@/components/diagram-nodes/ObjectStorageNode';

export default function WorkflowObjectStorageNode() {
  return (
    <div className="relative">
      <ObjectStorageNode variant="compact" />
      <Handle
        type="source"
        position={Position.Top}
        id="output-top"
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
      <Handle
        type="target"
        position={Position.Left}
        id="input-left"
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
    </div>
  );
}
