import { useMemo, useCallback } from "react";
import { ReactFlow, Node, Edge, Background, Controls } from "@xyflow/react";
import "@xyflow/react/dist/style.css";

import DocumentSourceNode, {
  DocumentSourceNodeData,
} from "@/components/diagram-nodes/rag/DocumentSourceNode";
import ChunkProcessorNode, {
  ChunkProcessorNodeData,
} from "@/components/diagram-nodes/rag/ChunkProcessorNode";
import VeloDBSearchNode, {
  VeloDBPanelType,
} from "@/components/diagram-nodes/rag/VeloDBSearchNode";
import ChatBotNode, {
  ChatBotNodeData,
} from "@/components/diagram-nodes/rag/ChatBotNode";

// Define node types outside component to prevent recreation
const nodeTypes = {
  documentSource: DocumentSourceNode,
  chunkProcessor: ChunkProcessorNode,
  velodbSearch: VeloDBSearchNode,
  chatBot: ChatBotNode,
};

// Panel type for the detail area
export type RAGPanelType = "documents" | "chunks" | "hybrid" | "context" | "graph" | "chat" | null;

export interface RAGPipelineDiagramProps {
  /** Callback when a panel should be opened */
  onPanelSelect: (panel: RAGPanelType) => void;
  /** Document corpus name to display */
  corpusName?: string;
  /** Number of documents in the corpus */
  documentCount?: number;
  /** Number of chunks processed */
  chunkCount?: number;
  /** Number of vectors (embeddings) stored */
  vectorCount?: number;
  /** Number of entities in knowledge graph */
  entityCount?: number;
  /** Embedding dimension (e.g., 1024 for BGE-M3) */
  embeddingDimension?: number;
  /** Whether chunks are currently being processed */
  isProcessing?: boolean;
  /** Last chat message to display */
  lastMessage?: string;
  /** Height of the diagram container */
  height?: number | string;
}

/**
 * RAGPipelineDiagram - React Flow diagram showing the RAG pipeline
 *
 * Displays 4 connected nodes: DOCS -> CHUNKS -> VELODB -> CHAT
 * - DocumentSourceNode: Shows corpus info, click to browse documents
 * - ChunkProcessorNode: Shows chunk stats, click to inspect chunks
 * - VeloDBSearchNode: Hero node with 3 capability buttons (Hybrid, Context, Graph)
 * - ChatBotNode: Shows chat preview, click to open chat interface
 */
const RAGPipelineDiagram = ({
  onPanelSelect,
  corpusName = "Demo Corpus",
  documentCount = 42,
  chunkCount = 1248,
  vectorCount = 0,
  entityCount = 0,
  embeddingDimension = 1024,
  isProcessing = false,
  lastMessage = "Ask me anything...",
  height = 300,
}: RAGPipelineDiagramProps) => {
  // Handler for VeloDB node button clicks
  const handleVeloDBPanelSelect = useCallback(
    (panel: VeloDBPanelType) => {
      onPanelSelect(panel);
    },
    [onPanelSelect]
  );

  // Handler for document node click
  const handleDocumentsClick = useCallback(() => {
    onPanelSelect("documents");
  }, [onPanelSelect]);

  // Handler for chunks node click
  const handleChunksClick = useCallback(() => {
    onPanelSelect("chunks");
  }, [onPanelSelect]);

  // Handler for chat node click
  const handleChatClick = useCallback(() => {
    onPanelSelect("chat");
  }, [onPanelSelect]);

  // Create nodes with data and callbacks
  const nodes = useMemo<Node[]>(() => {
    const docsNode: Node<DocumentSourceNodeData> = {
      id: "docs",
      type: "documentSource",
      position: { x: 50, y: 120 },
      data: {
        corpusName,
        documentCount,
        onClick: handleDocumentsClick,
      },
    };

    const chunksNode: Node<ChunkProcessorNodeData> = {
      id: "chunks",
      type: "chunkProcessor",
      position: { x: 250, y: 120 },
      data: {
        chunkCount,
        vectorCount,
        embeddingDimension,
        isProcessing,
        onClick: handleChunksClick,
      },
    };

    const velodbNode: Node<{ onPanelSelect?: (panel: VeloDBPanelType) => void }> = {
      id: "velodb",
      type: "velodbSearch",
      position: { x: 450, y: 100 },
      data: {
        onPanelSelect: handleVeloDBPanelSelect,
      },
    };

    const chatNode: Node<ChatBotNodeData> = {
      id: "chat",
      type: "chatBot",
      position: { x: 650, y: 120 },
      data: {
        lastMessage,
        onClick: handleChatClick,
      },
    };

    return [docsNode, chunksNode, velodbNode, chatNode];
  }, [
    corpusName,
    documentCount,
    chunkCount,
    vectorCount,
    embeddingDimension,
    isProcessing,
    lastMessage,
    handleDocumentsClick,
    handleChunksClick,
    handleVeloDBPanelSelect,
    handleChatClick,
  ]);

  // Define edges between nodes with animations
  const edges = useMemo<Edge[]>(
    () => [
      {
        id: "e-docs-chunks",
        source: "docs",
        target: "chunks",
        animated: true,
        style: { stroke: "#8b5cf6", strokeWidth: 2 },
        type: "smoothstep",
      },
      {
        id: "e-chunks-velodb",
        source: "chunks",
        target: "velodb",
        animated: true,
        style: { stroke: "#06b6d4", strokeWidth: 2 },
        type: "smoothstep",
      },
      {
        id: "e-velodb-chat",
        source: "velodb",
        target: "chat",
        animated: true,
        style: { stroke: "#22c55e", strokeWidth: 2 },
        type: "smoothstep",
      },
    ],
    []
  );

  return (
    <div
      className="border rounded-lg bg-white"
      style={{ height }}
      data-testid="rag-pipeline-diagram"
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        attributionPosition="bottom-left"
        minZoom={0.5}
        maxZoom={1.5}
        panOnScroll={false}
        zoomOnScroll={false}
        preventScrolling={false}
        defaultEdgeOptions={{
          type: "smoothstep",
          animated: true,
        }}
      >
        <Background gap={16} size={1} color="#E0E0E0" />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
};

export default RAGPipelineDiagram;
