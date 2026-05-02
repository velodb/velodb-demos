import { ReactFlow, Node, Edge, Background, Controls, Position } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { PostgresNode } from "../diagram-nodes/PostgresNode";
import { FlinkNode } from "../diagram-nodes/FlinkNode";
import { KafkaNode } from "../diagram-nodes/KafkaNode";
import { VeloDBNode } from "../diagram-nodes/VeloDBNode";
import { Catalogs } from "../diagram-nodes/Catalogs";
import { ClickstreamNode } from "../diagram-nodes/ClickstreamNode";
import { KibanaNode } from "../diagram-nodes/KibanaNode";
import { GrafanaNode } from "../diagram-nodes/GrafanaNode";
import { ObjectStorageNode } from "../diagram-nodes/ObjectStorageNode";

// Custom node components wrapper
const nodeTypes = {
  postgres: () => <PostgresNode variant="compact" showDetails={false} />,
  flinkCdc: () => <FlinkNode />,
  clickstream: () => <ClickstreamNode />,
  kafka: () => <KafkaNode />,
  catalogs: () => <Catalogs variant="compact" />,
  velodb: () => <VeloDBNode />,
  kibana: () => <KibanaNode />,
  grafana: () => <GrafanaNode />,
  objectStorage: () => <ObjectStorageNode variant="compact" />,
};

const nodes: Node[] = [
  // Left side - Sources
  { id: 'postgres', type: 'postgres', position: { x: 50, y: 50 }, data: {} },
  { id: 'clickstream', type: 'clickstream', position: { x: 50, y: 250 }, data: {} },

  // Middle-left - Processing
  { id: 'flinkCdc', type: 'flinkCdc', position: { x: 350, y: 50 }, data: {} },

  // Center - Event Hub
  { id: 'kafka', type: 'kafka', position: { x: 650, y: 150 }, data: {}, sourcePosition: Position.Right },

  // Middle-right - Storage/Catalog
  { id: 'catalogs', type: 'catalogs', position: { x: 650, y: 380 }, data: {}, targetPosition: Position.Top, sourcePosition: Position.Right },
  { id: 'objectStorage', type: 'objectStorage', position: { x: 1000, y: 380 }, data: {}, targetPosition: Position.Left, sourcePosition: Position.Top },

  // Right - Analytics Engine
  { id: 'velodb', type: 'velodb', position: { x: 1000, y: 220 }, data: {}, targetPosition: Position.Bottom, sourcePosition: Position.Right },

  // Far right - Visualization
  { id: 'kibana', type: 'kibana', position: { x: 1350, y: 150 }, data: {} },
  { id: 'grafana', type: 'grafana', position: { x: 1350, y: 350 }, data: {} },
];

const edges: Edge[] = [
  // Postgres -> Flink CDC (with CDC label)
  {
    id: 'e-postgres-flink',
    source: 'postgres',
    target: 'flinkCdc',
    label: 'CDC',
    animated: true,
    style: { stroke: '#1a73e8', strokeWidth: 2 },
    labelStyle: { fill: '#1a73e8', fontWeight: 600 }
  },

  // Flink CDC -> Kafka
  {
    id: 'e-flink-kafka',
    source: 'flinkCdc',
    target: 'kafka',
    animated: true,
    style: { stroke: '#ec4899', strokeWidth: 2 }
  },

  // Clickstream -> Kafka
  {
    id: 'e-clickstream-kafka',
    source: 'clickstream',
    target: 'kafka',
    animated: true,
    style: { stroke: '#a855f7', strokeWidth: 2 }
  },

  // Kafka -> VeloDB
  {
    id: 'e-kafka-velodb',
    source: 'kafka',
    target: 'velodb',
    animated: true,
    style: { stroke: '#231F20', strokeWidth: 2 }
  },

  // Kafka -> Data Catalogs
  {
    id: 'e-kafka-catalogs',
    source: 'kafka',
    target: 'catalogs',
    animated: true,
    style: { stroke: '#231F20', strokeWidth: 2 }
  },

  // Data Catalogs -> Object Storage
  {
    id: 'e-catalogs-objectStorage',
    source: 'catalogs',
    target: 'objectStorage',
    animated: true,
    style: { stroke: '#6366f1', strokeWidth: 2 }
  },

  // Object Storage -> VeloDB
  {
    id: 'e-objectStorage-velodb',
    source: 'objectStorage',
    target: 'velodb',
    animated: true,
    style: { stroke: '#6366f1', strokeWidth: 2 }
  },

  // VeloDB -> Kibana
  {
    id: 'e-velodb-kibana',
    source: 'velodb',
    target: 'kibana',
    animated: true,
    style: { stroke: '#1a73e8', strokeWidth: 2 }
  },

  // VeloDB -> Grafana
  {
    id: 'e-velodb-grafana',
    source: 'velodb',
    target: 'grafana',
    animated: true,
    style: { stroke: '#1a73e8', strokeWidth: 2 }
  },
];

export const CustomerFacingDiagramDetailed = () => {
  return (
    <div className="bg-white rounded-lg p-8">
      <div className="h-[700px] border border-[#E0E0E0] rounded-lg">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          fitView
          attributionPosition="bottom-left"
          minZoom={0.5}
          maxZoom={1.5}
          defaultEdgeOptions={{
            type: 'smoothstep',
            animated: true,
          }}
        >
          <Background gap={16} size={1} color="#E0E0E0" />
          <Controls />
        </ReactFlow>
      </div>

      <div className="mt-8 bg-white rounded-lg p-6 border border-[#E0E0E0]">
        <h3 className="font-semibold text-lg mb-3 text-[#333333]">Pipeline Overview</h3>
        <div className="grid grid-cols-2 gap-4 text-sm">
          <div className="space-y-2">
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">PostgreSQL</span> - Source database with CDC
            </p>
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">Flink CDC</span> - Real-time stream processing
            </p>
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">Clickstream</span> - Real-time event tracking
            </p>
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">Kafka</span> - Event streaming backbone
            </p>
          </div>
          <div className="space-y-2">
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">Data Catalogs</span> - Unity Catalog, Iceberg, AWS Glue
            </p>
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">Object Storage</span> - S3, GCS, Azure Blob
            </p>
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">VeloDB</span> - High-performance analytics engine
            </p>
            <p className="text-[#777777]">
              <span className="text-[#333333] font-medium">Kibana & Grafana</span> - Visualization and metrics
            </p>
          </div>
        </div>
      </div>
    </div>
  );
};
