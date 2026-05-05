import { useCallback } from 'react';
import {
  ReactFlow,
  MiniMap,
  Controls,
  Background,
  useNodesState,
  useEdgesState,
  addEdge,
  Connection,
  Edge,
  BackgroundVariant,
  Node,
  SmoothStepEdge,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import WorkflowPostgresNode from './nodes/WorkflowPostgresNode';
import WorkflowFlinkNode from './nodes/WorkflowFlinkNode';
import WorkflowClickstreamNode from './nodes/WorkflowClickstreamNode';
import WorkflowKafkaNode from './nodes/WorkflowKafkaNode';
import WorkflowCatalogsNode from './nodes/WorkflowCatalogsNode';
import WorkflowVeloDBNode from './nodes/WorkflowVeloDBNode';
import WorkflowKibanaNode from './nodes/WorkflowKibanaNode';
import WorkflowGrafanaNode from './nodes/WorkflowGrafanaNode';
import WorkflowObjectStorageNode from './nodes/WorkflowObjectStorageNode';

const nodeTypes = {
  postgres: WorkflowPostgresNode,
  flinkCdc: WorkflowFlinkNode,
  clickstream: WorkflowClickstreamNode,
  kafka: WorkflowKafkaNode,
  catalogs: WorkflowCatalogsNode,
  velodb: WorkflowVeloDBNode,
  kibana: WorkflowKibanaNode,
  grafana: WorkflowGrafanaNode,
  objectStorage: WorkflowObjectStorageNode,
};

const initialNodes: Node[] = [
  // Layer 1: Data Sources
  {
    id: 'postgres',
    type: 'postgres',
    position: { x: -491.902, y: -495.727 },
    data: { label: 'PostgreSQL' },
  },
  {
    id: 'clickstream',
    type: 'clickstream',
    position: { x: -475.05506265353523, y: -246.8868120393942 },
    data: { label: 'Clickstream' },
  },

  // Layer 2: Stream Processing
  {
    id: 'flinkCdc',
    type: 'flinkCdc',
    position: { x: 30.73, y: -499.937 },
    data: { label: 'Flink CDC', description: 'Change Data Capture' },
  },

  // Layer 3: Message Queue (centered)
  {
    id: 'kafka',
    type: 'kafka',
    position: { x: 34.662593843827125, y: -301.2025309173402 },
    data: { label: 'Kafka', description: 'Event Streaming Platform' },
  },

  // Layer 3: Data Catalogs (below Kafka)
  {
    id: 'catalogs',
    type: 'catalogs',
    position: { x: -2.6459999999999866, y: 154.49209479915768 },
    data: { label: 'Data Catalogs' },
  },

  // Layer 3: Object Storage (right of Data Catalogs, below VeloDB)
  {
    id: 'objectStorage',
    type: 'objectStorage',
    position: { x: 716.056749931762, y: 153.73409479915773 },
    data: { label: 'Object Storage' },
  },

  // Layer 4: Analytics Engine (centered)
  {
    id: 'velodb',
    type: 'velodb',
    position: { x: 740.699, y: -450.064 },
    data: { label: 'VeloDB', description: 'High-performance analytics engine' },
  },

  // Layer 5: Visualization
  {
    id: 'kibana',
    type: 'kibana',
    position: { x: 1319.957, y: -464.493 },
    data: { label: 'Kibana' },
  },
  {
    id: 'grafana',
    type: 'grafana',
    position: { x: 1330.441, y: -46.035 },
    data: { label: 'Grafana' },
  },
];

const edgeTypes = {
  smoothstep: (props: any) => <SmoothStepEdge {...props} borderRadius={10} />,
};

const initialEdges: Edge[] = [
  // Postgres -> Flink CDC
  {
    id: 'e-postgres-flink',
    source: 'postgres',
    target: 'flinkCdc',
    label: 'CDC',
    animated: true,
    type: 'smoothstep',
    style: { stroke: '#1a73e8', strokeWidth: 2.5 },
    labelStyle: { fill: '#1a73e8', fontWeight: 600, fontSize: 12 },
    labelBgStyle: { fill: '#ffffff', fillOpacity: 0.9 },
  },

  // Flink CDC -> VeloDB
  {
    id: 'e-flink-velodb',
    source: 'flinkCdc',
    target: 'velodb',
    type: 'smoothstep',
    targetHandle: 'input-left-upper',
    animated: true,
    style: { stroke: '#ec4899', strokeWidth: 2.5 },
  },

  // Clickstream -> Kafka
  {
    id: 'e-clickstream-kafka',
    source: 'clickstream',
    target: 'kafka',
    type: 'smoothstep',
    targetHandle: 'input-2',
    animated: true,
    style: { stroke: '#a855f7', strokeWidth: 2.5 },
  },

  // Kafka -> VeloDB
  {
    id: 'e-kafka-velodb',
    source: 'kafka',
    sourceHandle: 'output-1',
    target: 'velodb',
    type: 'smoothstep',
    targetHandle: 'input-left-lower',
    animated: true,
    style: { stroke: '#231F20', strokeWidth: 2.5 },
  },

  // Kafka -> Data Catalogs
  {
    id: 'e-kafka-catalogs',
    source: 'kafka',
    sourceHandle: 'output-2',
    target: 'catalogs',
    type: 'smoothstep',
    animated: true,
    style: { stroke: '#231F20', strokeWidth: 2.5, strokeDasharray: '6 4' },
  },

  // Data Catalogs (top) -> VeloDB (bottom, catalogs dot)
  {
    id: 'e-catalogs-velodb-direct',
    source: 'catalogs',
    sourceHandle: 'output-top',
    target: 'velodb',
    targetHandle: 'input-bottom-catalogs',
    type: 'smoothstep',
    animated: true,
    style: { stroke: '#6366f1', strokeWidth: 2.5 },
  },

  // Data Catalogs -> Object Storage
  {
    id: 'e-catalogs-objectStorage',
    source: 'catalogs',
    sourceHandle: 'output-right',
    target: 'objectStorage',
    targetHandle: 'input-left',
    type: 'smoothstep',
    animated: true,
    style: { stroke: '#6366f1', strokeWidth: 2.5, strokeDasharray: '6 4' },
  },

  // Object Storage -> VeloDB (from top of Object Storage to bottom of VeloDB)
  {
    id: 'e-objectStorage-velodb',
    source: 'objectStorage',
    sourceHandle: 'output-top',
    target: 'velodb',
    targetHandle: 'input-bottom-objectStorage',
    type: 'smoothstep',
    animated: true,
    style: { stroke: '#6366f1', strokeWidth: 2.5 },
  },

  // VeloDB -> Kibana
  {
    id: 'e-velodb-kibana',
    source: 'velodb',
    sourceHandle: 'output-right',
    target: 'kibana',
    type: 'smoothstep',
    animated: true,
    style: { stroke: '#1a73e8', strokeWidth: 2.5 },
  },

  // VeloDB -> Grafana
  {
    id: 'e-velodb-grafana',
    source: 'velodb',
    sourceHandle: 'output-right',
    target: 'grafana',
    type: 'smoothstep',
    animated: true,
    style: { stroke: '#1a73e8', strokeWidth: 2.5 },
  },
];

export default function WorkflowCanvas() {
  const [nodes, setNodes, onNodesChange] = useNodesState(initialNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(initialEdges);

  const onConnect = useCallback(
    (params: Connection | Edge) => setEdges((eds) => addEdge(params, eds)),
    [setEdges],
  );

  return (
    <div className="h-screen w-full bg-white">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        defaultViewport={{
          x: 702.7550922427879,
          y: 548.3488429562618,
          zoom: 0.7941431993240515,
        }}
        defaultEdgeOptions={{
          type: 'smoothstep',
          animated: true,
        }}
        nodesDraggable={true}
        nodesConnectable={false}
        elementsSelectable={true}
        className="bg-white"
        minZoom={0.3}
        maxZoom={1.5}
      >
        <Controls className="bg-card border-border" />
        <MiniMap
          className="bg-card border-border"
          nodeColor={(node) => {
            if (node.type === 'postgres') return '#1a73e8';
            if (node.type === 'flinkCdc') return '#ec4899';
            if (node.type === 'clickstream') return '#a855f7';
            if (node.type === 'kafka') return '#231F20';
            if (node.type === 'catalogs') return '#6366f1';
            if (node.type === 'objectStorage') return '#6366f1';
            if (node.type === 'velodb') return '#6366f1';
            if (node.type === 'kibana') return '#005571';
            if (node.type === 'grafana') return '#f46800';
            return '#6366f1';
          }}
        />
        <Background
          variant={BackgroundVariant.Dots}
          gap={16}
          size={1}
          color="#E0E0E0"
        />
      </ReactFlow>
    </div>
  );
}
