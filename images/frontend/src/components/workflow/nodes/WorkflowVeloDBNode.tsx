import { Handle, Position } from '@xyflow/react';
import { VeloDBNode } from '@/components/diagram-nodes/VeloDBNode';

export default function WorkflowVeloDBNode() {
  return (
    <div className="relative">
      <VeloDBNode showDetails={false} />
      <Handle
        type="target"
        position={Position.Left}
        id="input-left-upper"
        style={{ top: '35%' }}
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
      <Handle
        type="target"
        position={Position.Left}
        id="input-left-lower"
        style={{ top: '65%' }}
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
      <Handle
        type="target"
        position={Position.Bottom}
        id="input-bottom-catalogs"
        style={{ left: '40%' }}
        className="w-3 h-3 !bg-indigo-500 border-2 border-white"
      />
      <Handle
        type="target"
        position={Position.Bottom}
        id="input-bottom-objectStorage"
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
