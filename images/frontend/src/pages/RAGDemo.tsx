import { useState, useCallback, useEffect } from "react";
import { Sidebar } from "@/components/Sidebar";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { FileText, Layers, Database, MessageSquare } from "lucide-react";
import { RAG_API_BASE_URL } from "@/lib/utils";
import RAGPipelineDiagram, { RAGPanelType } from "@/components/diagrams/RAGPipelineDiagram";
import HybridSearchPanel from "@/components/rag/velodb-panels/HybridSearchPanel";
import ContextEngineeringPanel from "@/components/rag/velodb-panels/ContextEngineeringPanel";
import KnowledgeGraphPanel from "@/components/rag/velodb-panels/KnowledgeGraphPanel";
import ChatInterface from "@/components/rag/ChatInterface";
import { DocumentBrowser } from "@/components/rag/DocumentBrowser";
import { ChunkInspector } from "@/components/rag/ChunkInspector";
import { TenantSelector } from "@/components/rag/TenantSelector";

// Panel types for the detail area
type PanelType = RAGPanelType;

interface CorpusSummary {
  corpus_id: string;
  name: string | null;
  document_count: number;
  chunk_count: number;
  vector_count?: number;
  entity_count?: number;
  embedding_dimension?: number;
  embedding_model?: string;
  vector_index_type?: string;
}

// Tenant to corpus mapping
const TENANT_CORPUS_MAP: Record<string, string> = {
  "VeloDB Sample": "velodb_docs",
  "default": "default",
  "acme-corp": "acme_docs",
};

const RAGDemo = () => {
  const [activePanel, setActivePanel] = useState<PanelType>(null);
  const [tenantId, setTenantId] = useState("VeloDB Sample");
  const corpusId = TENANT_CORPUS_MAP[tenantId] || "default";
  const [documentCount, setDocumentCount] = useState(0);
  const [chunkCount, setChunkCount] = useState(0);
  const [vectorCount, setVectorCount] = useState(0);
  const [entityCount, setEntityCount] = useState(0);
  const [embeddingDimension, setEmbeddingDimension] = useState(1024);
  const [corpusName, setCorpusName] = useState("Loading...");

  // Fetch real counts from API
  useEffect(() => {
    const fetchCounts = async () => {
      try {
        const response = await fetch(
          `${RAG_API_BASE_URL}/api/v1/tenants/${encodeURIComponent(tenantId)}/corpora`
        );
        if (response.ok) {
          const data = await response.json();
          const corpora: CorpusSummary[] = data.corpora || [];

          // Sum up counts from all corpora
          const totalDocs = corpora.reduce((sum, c) => sum + (c.document_count || 0), 0);
          const totalChunks = corpora.reduce((sum, c) => sum + (c.chunk_count || 0), 0);
          const totalVectors = corpora.reduce((sum, c) => sum + (c.vector_count || 0), 0);
          const totalEntities = corpora.reduce((sum, c) => sum + (c.entity_count || 0), 0);

          setDocumentCount(totalDocs);
          setChunkCount(totalChunks);
          setVectorCount(totalVectors);
          setEntityCount(totalEntities);

          // Get embedding dimension from first corpus with data
          if (corpora.length > 0) {
            const primaryCorpus = corpora.find(c => c.document_count > 0) || corpora[0];
            setEmbeddingDimension(primaryCorpus.embedding_dimension || 1024);
          }

          // Use first corpus name or default
          if (corpora.length > 0) {
            const primaryCorpus = corpora.find(c => c.document_count > 0) || corpora[0];
            setCorpusName(primaryCorpus.name || primaryCorpus.corpus_id || "Demo Corpus");
          } else {
            setCorpusName("No Corpus");
          }
        }
      } catch (err) {
        console.error("Failed to fetch corpus stats:", err);
        setCorpusName("Demo Corpus");
      }
    };
    fetchCounts();
  }, [tenantId]);

  // Callback for panel selection from diagram
  const handlePanelSelect = useCallback((panel: RAGPanelType) => {
    setActivePanel(panel);
  }, []);

  const renderPanel = () => {
    switch (activePanel) {
      case "documents":
        return <DocumentBrowser tenantId={tenantId} />;
      case "chunks":
        return <ChunkInspector tenantId={tenantId} corpusId={corpusId} />;
      case "hybrid":
        return <HybridSearchPanel />;
      case "context":
        return <ContextEngineeringPanel tenantId={tenantId} corpusId={corpusId} />;
      case "graph":
        return <KnowledgeGraphPanel />;
      case "chat":
        return <ChatInterface tenantId={tenantId} corpusId={corpusId} />;
      default:
        return (
          <Card>
            <CardHeader>
              <CardTitle>Welcome to RAG Demo</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-muted-foreground mb-4">
                This demo showcases VeloDB's RAG (Retrieval-Augmented Generation) capabilities:
              </p>
              <ul className="space-y-2 text-sm text-muted-foreground">
                <li className="flex items-center gap-2">
                  <FileText className="w-4 h-4 text-blue-600" />
                  <span><strong>Documents:</strong> Browse and select document corpora</span>
                </li>
                <li className="flex items-center gap-2">
                  <Layers className="w-4 h-4 text-purple-600" />
                  <span><strong>Chunks:</strong> Inspect text chunks and their embeddings</span>
                </li>
                <li className="flex items-center gap-2">
                  <Database className="w-4 h-4 text-cyan-600" />
                  <span><strong>VeloDB:</strong> Hybrid search, context engineering, knowledge graph</span>
                </li>
                <li className="flex items-center gap-2">
                  <MessageSquare className="w-4 h-4 text-green-600" />
                  <span><strong>Chat:</strong> Ask questions and get cited answers</span>
                </li>
              </ul>
              <p className="mt-4 text-sm text-muted-foreground">
                Click on any node in the diagram above to explore its capabilities.
              </p>
            </CardContent>
          </Card>
        );
    }
  };

  return (
    <div className="flex h-screen bg-background">
      <Sidebar />
      <main className="flex-1 overflow-hidden flex flex-col">
        {/* Header */}
        <div className="p-6 border-b">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-2xl font-bold text-foreground">RAG Pipeline Demo</h1>
              <p className="text-muted-foreground text-sm mt-1">
                Multimodal Retrieval-Augmented Generation powered by VeloDB
              </p>
            </div>
            <TenantSelector value={tenantId} onChange={setTenantId} />
          </div>
        </div>

        {/* Content Area */}
        <div className="flex-1 flex flex-col overflow-hidden">
          {/* Diagram Area - Fixed height */}
          <div className="flex-shrink-0 p-4 pb-2">
            <RAGPipelineDiagram
              onPanelSelect={handlePanelSelect}
              corpusName={corpusName}
              documentCount={documentCount}
              chunkCount={chunkCount}
              vectorCount={vectorCount}
              entityCount={entityCount}
              embeddingDimension={embeddingDimension}
              isProcessing={false}
              lastMessage="Ask me anything..."
              height={220}
            />
          </div>

          {/* Panel Area - Flexible height */}
          <div className="flex-1 min-h-0 px-4 pb-4 overflow-y-auto">
            {renderPanel()}
          </div>
        </div>
      </main>
    </div>
  );
};

export default RAGDemo;
