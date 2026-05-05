import { Handle, Position } from '@xyflow/react';
import { Catalogs } from '@/components/diagram-nodes/Catalogs';

export default function WorkflowCatalogsNode() {
  return (
    <div className="relative">
      <Catalogs variant="compact" />
      <Handle
        type="target"
        position={Position.Top}
        id="input-top"
        style={{ left: '40%' }}
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
      <Handle
        type="source"
        position={Position.Top}
        id="output-top"
        style={{ left: '60%' }}
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
      <Handle
        type="source"
        position={Position.Right}
        id="output-right"
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
    </div>
  );
}
