import { Handle, Position } from '@xyflow/react';
import { KafkaNode } from '@/components/diagram-nodes/KafkaNode';

export default function WorkflowKafkaNode() {
  return (
    <div className="relative">
      <KafkaNode />
      <Handle
        type="target"
        position={Position.Left}
        id="input-1"
        style={{ top: '30%' }}
        className="w-3 h-3 !bg-gray-800 border-2 border-white"
      />
      <Handle
        type="target"
        position={Position.Left}
        id="input-2"
        style={{ top: '70%' }}
        className="w-3 h-3 !bg-gray-800 border-2 border-white"
      />
      <Handle
        type="source"
        position={Position.Right}
        id="output-1"
        style={{ top: '35%' }}
        className="w-3 h-3 !bg-gray-800 border-2 border-white"
      />
      <Handle
        type="source"
        position={Position.Bottom}
        id="output-2"
        className="w-3 h-3 !bg-gray-800 border-2 border-white"
      />
    </div>
  );
}
