import { useState, useCallback, useEffect, useRef } from "react";
import ForceGraph2D, { NodeObject, LinkObject } from "react-force-graph-2d";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  Share2, Search, X, FileText, Database, Loader2, RefreshCw,
  Image, Table2, Code2, Calculator, ChevronRight, HelpCircle,
  Cpu, GitBranch, Zap, ArrowRight, CheckCircle2
} from "lucide-react";
import { cn, getAssetUrl, MultimodalData, RAG_API_BASE_URL as API_BASE_URL } from "@/lib/utils";

// Entity type colors - matches types returned by API
const ENTITY_COLORS: Record<string, string> = {
  // Database/Data types
  TABLE: "#8b5cf6",           // purple - database tables
  COLUMN: "#a78bfa",          // light purple - table columns
  DATABASE: "#8b5cf6",        // purple
  DATASET: "#7c3aed",         // deep purple - datasets

  // Operations/Methods
  METHOD: "#3b82f6",          // blue - operations/methods
  SQL_OPERATION: "#3b82f6",   // blue
  BENCHMARK: "#60a5fa",       // light blue - benchmarks

  // Configuration/Parameters
  PARAMETER: "#f97316",       // orange - parameters/configs
  CONFIGURATION_FILE: "#fb923c", // light orange
  METRIC: "#ea580c",          // dark orange - metrics

  // Software/Components
  COMPONENT: "#10b981",       // green - components
  SOFTWARE: "#059669",        // dark green - software
  TOOL: "#34d399",            // light green - tools

  // Infrastructure
  OPERATING_SYSTEM: "#06b6d4", // cyan - OS
  MODEL: "#0891b2",           // dark cyan - models/architectures
  SCHEMA: "#22d3d8",          // light cyan - schemas

  // Concepts/Other
  CONCEPT: "#f59e0b",         // amber - general concepts
  INTERFACE: "#eab308",       // yellow - interfaces
  FILE_FORMAT: "#ec4899",     // pink - file formats

  default: "#6b7280",         // gray - unknown types
};

interface GraphNode extends NodeObject {
  id: string;
  name: string;
  type: string;
  chunkCount: number;
  relCount: number;
  val: number; // node size
  color: string;
}

interface GraphLink extends LinkObject {
  source: string;
  target: string;
  relation: string;
}

interface EntityDetail {
  entity_id: string;
  entity_name: string;
  entity_type: string | null;
  description: string | null;
  related_entities: Array<{
    entity_name: string;
    relationship: string;
    direction: string;
  }>;
  linked_chunks: Array<{
    chunk_id: string;
    doc_id: string;
    chunk_index: number;
    content: string;
    content_type: string;
    page_number: number | null;
  }>;
  multimodal_content: Array<{
    type: string;
    description: string;
    image_path?: string;
    caption?: string;
    content?: string;
    multimodal_data?: MultimodalData;
  }>;
}

interface KnowledgeGraphPanelProps {
  tenantId?: string;
  corpusId?: string;
}

const KnowledgeGraphPanel = ({ tenantId = "VeloDB Sample", corpusId = "velodb_docs" }: KnowledgeGraphPanelProps) => {
  const graphRef = useRef<any>();
  const [graphData, setGraphData] = useState<{ nodes: GraphNode[]; links: GraphLink[] }>({ nodes: [], links: [] });
  const [searchQuery, setSearchQuery] = useState("");
  const [hoveredNode, setHoveredNode] = useState<GraphNode | null>(null);
  const [selectedEntity, setSelectedEntity] = useState<EntityDetail | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isLoadingDetails, setIsLoadingDetails] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [stats, setStats] = useState({ entities: 0, relationships: 0 });
  const [dialogOpen, setDialogOpen] = useState(false);
  const [helpDialogOpen, setHelpDialogOpen] = useState(false);


  // Fetch graph data
  const fetchGraphData = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const response = await fetch(
        `${API_BASE_URL}/api/v1/demo/knowledge-graph?tenant_id=${encodeURIComponent(tenantId)}&corpus_id=${encodeURIComponent(corpusId)}&limit=50`
      );
      if (!response.ok) throw new Error("Failed to fetch");
      const data = await response.json();

      const entities = data.entities || [];
      const relationships = data.relationships || [];

      // Build nodes
      const nodes: GraphNode[] = entities.map((e: any) => ({
        id: e.entity_id,
        name: e.entity_name,
        type: e.entity_type || "CONCEPT",
        chunkCount: e.chunk_count || 0,
        relCount: e.relationship_count || 0,
        val: Math.max(3, Math.min(15, (e.relationship_count || 1) * 2)),
        color: ENTITY_COLORS[e.entity_type] || ENTITY_COLORS.default,
      }));

      // Build links - filter to only include nodes that exist
      const nodeIds = new Set(nodes.map(n => n.id));
      const links: GraphLink[] = relationships
        .filter((r: any) => {
          const sourceId = r.source.replace(/ /g, "_");
          const targetId = r.target.replace(/ /g, "_");
          return nodeIds.has(sourceId) && nodeIds.has(targetId);
        })
        .map((r: any) => ({
          source: r.source.replace(/ /g, "_"),
          target: r.target.replace(/ /g, "_"),
          relation: r.relation_type,
        }));

      setGraphData({ nodes, links });
      setStats({
        entities: data.total_entities || entities.length,
        relationships: data.total_relationships || relationships.length,
      });
    } catch (err) {
      console.error(err);
      setError("Failed to load graph. Is the API running?");
    } finally {
      setIsLoading(false);
    }
  }, [tenantId, corpusId]);

  useEffect(() => {
    fetchGraphData();
  }, [fetchGraphData]);


  // Handle node click - fetch details and show multimodal content
  const handleNodeClick = useCallback(async (node: GraphNode) => {
    setIsLoadingDetails(true);
    setDialogOpen(true);
    try {
      const response = await fetch(
        `${API_BASE_URL}/api/v1/demo/entities/${encodeURIComponent(node.name)}?tenant_id=${encodeURIComponent(tenantId)}&corpus_id=${encodeURIComponent(corpusId)}`
      );
      if (response.ok) {
        const data = await response.json();
        setSelectedEntity(data);
      }
    } catch (err) {
      console.error(err);
    } finally {
      setIsLoadingDetails(false);
    }
  }, [tenantId, corpusId]);

  // Search filter
  const filteredData = searchQuery.trim()
    ? {
        nodes: graphData.nodes.filter(n =>
          n.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
          n.type.toLowerCase().includes(searchQuery.toLowerCase())
        ),
        links: graphData.links.filter(l => {
          const query = searchQuery.toLowerCase();
          const sourceNode = graphData.nodes.find(n => n.id === l.source);
          const targetNode = graphData.nodes.find(n => n.id === l.target);
          return (
            sourceNode?.name.toLowerCase().includes(query) ||
            targetNode?.name.toLowerCase().includes(query)
          );
        }),
      }
    : graphData;

  // Content type icon
  const getContentIcon = (type: string) => {
    switch (type) {
      case "image": return <Image className="w-4 h-4 text-green-600" />;
      case "table": return <Table2 className="w-4 h-4 text-amber-600" />;
      case "code": return <Code2 className="w-4 h-4 text-blue-600" />;
      case "formula": return <Calculator className="w-4 h-4 text-purple-600" />;
      default: return <FileText className="w-4 h-4 text-gray-600" />;
    }
  };

  return (
    <Card className="h-full flex flex-col">
      <CardHeader className="pb-2 flex-shrink-0">
        <CardTitle className="flex items-center gap-2 text-lg">
          <Share2 className="w-5 h-5 text-cyan-600" />
          Knowledge Graph
          <Badge variant="outline" className="ml-2 text-xs font-normal">
            {stats.entities} entities, {stats.relationships} relations
          </Badge>
          <button
            onClick={() => setHelpDialogOpen(true)}
            className="ml-auto p-1.5 rounded-full hover:bg-cyan-100 transition-colors group"
            title="How does Knowledge Graph work?"
          >
            <HelpCircle className="w-5 h-5 text-cyan-600 group-hover:text-cyan-700" />
          </button>
        </CardTitle>
      </CardHeader>

      <CardContent className="flex-1 flex flex-col gap-2 min-h-0">
        {/* Entity Graph Content */}
        <div className="flex-1 flex flex-col gap-2 min-h-0">
            {/* Search */}
            <div className="flex gap-2 flex-shrink-0">
              <Input
                placeholder="Search entities..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="flex-1 h-8 text-sm"
              />
              <Button variant="outline" size="icon" onClick={fetchGraphData} className="h-8 w-8">
                <RefreshCw className="w-3 h-3" />
              </Button>
            </div>

            {/* Legend - shows only types present in current graph */}
            <div className="flex flex-wrap gap-1.5 text-[10px] flex-shrink-0">
              {(() => {
                // Get unique types from current nodes
                const typesInGraph = [...new Set(graphData.nodes.map(n => n.type))];
                return typesInGraph.slice(0, 8).map((type) => (
                  <div key={type} className="flex items-center gap-1">
                    <div className="w-2 h-2 rounded-full" style={{ backgroundColor: ENTITY_COLORS[type] || ENTITY_COLORS.default }} />
                    <span className="text-muted-foreground">{type.replace(/_/g, " ")}</span>
                  </div>
                ));
              })()}
            </div>

            {/* Graph Container */}
            <div className="flex-1 border rounded-lg bg-slate-50 relative overflow-hidden min-h-[350px]">
          {isLoading ? (
            <div className="absolute inset-0 flex items-center justify-center">
              <Loader2 className="w-8 h-8 animate-spin text-cyan-600" />
            </div>
          ) : error ? (
            <div className="absolute inset-0 flex items-center justify-center">
              <div className="text-center">
                <p className="text-red-500 mb-2">{error}</p>
                <Button size="sm" onClick={fetchGraphData}>Retry</Button>
              </div>
            </div>
          ) : (
            <ForceGraph2D
              ref={graphRef}
              graphData={filteredData}
              nodeLabel={(node: any) => ""}
              nodeColor={(node: any) => node.color}
              nodeVal={(node: any) => node.val}
              linkColor={() => "#cbd5e1"}
              linkWidth={1}
              linkDirectionalArrowLength={4}
              linkDirectionalArrowRelPos={1}
              onNodeHover={(node: any) => setHoveredNode(node)}
              onNodeClick={(node: any) => handleNodeClick(node)}
              nodeCanvasObject={(node: any, ctx, globalScale) => {
                const label = node.name;
                const fontSize = Math.max(10 / globalScale, 3);
                const nodeSize = node.val;

                // Draw node circle
                ctx.beginPath();
                ctx.arc(node.x, node.y, nodeSize, 0, 2 * Math.PI);
                ctx.fillStyle = node.color;
                ctx.fill();

                // Draw border if hovered
                if (hoveredNode?.id === node.id) {
                  ctx.strokeStyle = "#000";
                  ctx.lineWidth = 2 / globalScale;
                  ctx.stroke();
                }

                // Draw label
                ctx.font = `${fontSize}px Sans-Serif`;
                ctx.textAlign = "center";
                ctx.textBaseline = "middle";
                ctx.fillStyle = "#374151";
                ctx.fillText(label, node.x, node.y + nodeSize + fontSize);
              }}
              nodePointerAreaPaint={(node: any, color, ctx) => {
                ctx.beginPath();
                ctx.arc(node.x, node.y, node.val + 5, 0, 2 * Math.PI);
                ctx.fillStyle = color;
                ctx.fill();
              }}
              cooldownTicks={100}
              onEngineStop={() => graphRef.current?.zoomToFit(400, 50)}
            />
          )}

          {/* Hover Tooltip */}
          {hoveredNode && (
            <div className="absolute top-3 left-3 bg-white rounded-lg shadow-lg border p-3 max-w-[250px] z-10">
              <div className="font-semibold text-sm mb-1">{hoveredNode.name}</div>
              <div className="flex items-center gap-2 mb-2">
                <Badge
                  variant="outline"
                  className="text-[10px]"
                  style={{ borderColor: hoveredNode.color, color: hoveredNode.color }}
                >
                  {hoveredNode.type}
                </Badge>
              </div>
              <div className="text-xs text-muted-foreground space-y-1">
                <div className="flex justify-between">
                  <span>Relationships:</span>
                  <span className="font-medium">{hoveredNode.relCount}</span>
                </div>
                <div className="flex justify-between">
                  <span>Chunks:</span>
                  <span className="font-medium">{hoveredNode.chunkCount}</span>
                </div>
              </div>
              <div className="text-[10px] text-muted-foreground mt-2 pt-2 border-t">
                Click to view multimodal content
              </div>
            </div>
          )}
            </div>

            {/* Info */}
            <div className="flex items-center gap-2 text-[10px] text-muted-foreground flex-shrink-0">
              <Database className="w-3 h-3" />
              <span>Hover for details, click for entity information</span>
            </div>
        </div>
      </CardContent>

      {/* Multimodal Content Dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-2xl max-h-[80vh] overflow-hidden flex flex-col">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Share2 className="w-5 h-5 text-cyan-600" />
              {selectedEntity?.entity_name || "Loading..."}
              {selectedEntity?.entity_type && (
                <Badge variant="outline" className="ml-2 text-xs">
                  {selectedEntity.entity_type}
                </Badge>
              )}
            </DialogTitle>
          </DialogHeader>

          {isLoadingDetails ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-6 h-6 animate-spin text-cyan-600" />
            </div>
          ) : selectedEntity ? (
            <ScrollArea className="flex-1 pr-4">
              <div className="space-y-4">
                {/* Description */}
                {selectedEntity.description && (
                  <div className="text-sm text-muted-foreground bg-slate-50 rounded-lg p-3">
                    {selectedEntity.description}
                  </div>
                )}

                {/* Related Entities */}
                {selectedEntity.related_entities.length > 0 && (
                  <div>
                    <h4 className="text-sm font-semibold mb-2 flex items-center gap-1">
                      <ChevronRight className="w-4 h-4" />
                      Related Entities ({selectedEntity.related_entities.length})
                    </h4>
                    <div className="grid grid-cols-2 gap-2">
                      {selectedEntity.related_entities.slice(0, 8).map((rel, i) => (
                        <div key={i} className="bg-slate-50 rounded p-2 text-xs">
                          <div className="font-medium">{rel.entity_name}</div>
                          <div className="text-muted-foreground flex items-center gap-1">
                            <Badge variant="secondary" className="text-[9px] px-1">
                              {rel.relationship.replace(/_/g, " ")}
                            </Badge>
                            <span className="text-[10px]">({rel.direction})</span>
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* Multimodal Content */}
                {selectedEntity.multimodal_content.length > 0 && (
                  <div>
                    <h4 className="text-sm font-semibold mb-2 flex items-center gap-1">
                      <Image className="w-4 h-4 text-green-600" />
                      Multimodal Content ({selectedEntity.multimodal_content.length})
                    </h4>
                    <div className="space-y-2">
                      {selectedEntity.multimodal_content.map((mm, i) => (
                        <div key={i} className="border rounded-lg p-3 bg-white">
                          <div className="flex items-center gap-2 mb-2">
                            {getContentIcon(mm.type)}
                            <span className="font-medium capitalize">{mm.type}</span>
                            {(mm.image_path || mm.multimodal_data?.path) && (
                              <span className="text-xs text-muted-foreground ml-auto">
                                {(mm.multimodal_data?.path || mm.image_path || "").split("/").pop()}
                              </span>
                            )}
                          </div>
                          {(mm.image_path || mm.multimodal_data) && (
                            <img
                              src={getAssetUrl(mm.multimodal_data || mm.image_path, tenantId, corpusId)}
                              alt={mm.caption || mm.description}
                              className="max-h-[200px] rounded border object-contain mx-auto"
                              onError={(e) => {
                                (e.target as HTMLImageElement).style.display = "none";
                              }}
                            />
                          )}
                                                    {/* Render table/code content as markdown */}
                          {(mm.type === 'table' || mm.type === 'code') && mm.content ? (
                            <div className="prose prose-sm max-w-none mt-2 overflow-x-auto">
                              <ReactMarkdown
                                remarkPlugins={[remarkGfm]}
                                components={{
                                  table: ({ children }) => (
                                    <table className="min-w-full border-collapse border border-slate-300 text-xs">
                                      {children}
                                    </table>
                                  ),
                                  th: ({ children }) => (
                                    <th className="border border-slate-300 bg-slate-100 px-2 py-1 text-left font-semibold">
                                      {children}
                                    </th>
                                  ),
                                  td: ({ children }) => (
                                    <td className="border border-slate-300 px-2 py-1">{children}</td>
                                  ),
                                  code: ({ className, children }) => (
                                    <code className={cn("bg-slate-900 text-slate-100 p-2 rounded text-xs block overflow-x-auto", className)}>
                                      {children}
                                    </code>
                                  ),
                                }}
                              >
                                {mm.content}
                              </ReactMarkdown>
                            </div>
                          ) : (mm.caption || mm.description) ? (
                            <p className="text-sm text-muted-foreground mt-2">
                              {mm.caption || mm.description}
                            </p>
                          ) : null}
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* Source Chunks */}
                {selectedEntity.linked_chunks.length > 0 && (
                  <div>
                    <h4 className="text-sm font-semibold mb-2 flex items-center gap-1">
                      <FileText className="w-4 h-4" />
                      Source Chunks ({selectedEntity.linked_chunks.length})
                    </h4>
                    <div className="space-y-2">
                      {selectedEntity.linked_chunks.slice(0, 5).map((chunk, i) => (
                        <div key={i} className="border rounded-lg p-3 bg-white text-sm">
                          <div className="flex items-center gap-2 mb-2 text-xs text-muted-foreground">
                            {getContentIcon(chunk.content_type)}
                            <span className="truncate">{chunk.doc_id}</span>
                            <Badge variant="outline" className="text-[10px] ml-auto">
                              #{chunk.chunk_index}
                            </Badge>
                          </div>
                          <p className="text-foreground line-clamp-3">{chunk.content}</p>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* Empty State */}
                {selectedEntity.related_entities.length === 0 &&
                  selectedEntity.multimodal_content.length === 0 &&
                  selectedEntity.linked_chunks.length === 0 && (
                    <div className="text-center py-8 text-muted-foreground">
                      No additional content available for this entity.
                    </div>
                  )}
              </div>
            </ScrollArea>
          ) : null}
        </DialogContent>
      </Dialog>

      {/* Educational Help Dialog */}
      <Dialog open={helpDialogOpen} onOpenChange={setHelpDialogOpen}>
        <DialogContent className="max-w-4xl max-h-[90vh] p-0 overflow-hidden">
          <div className="p-6 pb-0">
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2 text-xl">
                <HelpCircle className="w-6 h-6 text-cyan-600" />
                How Knowledge Graphs Power Better RAG
              </DialogTitle>
            </DialogHeader>
          </div>

          <div className="overflow-y-auto px-6 pb-6" style={{ maxHeight: "calc(90vh - 80px)" }}>
            <div className="space-y-6">

              {/* Why Knowledge Graphs */}
              <section className="space-y-3">
                <h3 className="font-semibold text-lg flex items-center gap-2 text-cyan-700">
                  <Zap className="w-5 h-5" />
                  Why Knowledge Graphs for RAG?
                </h3>
                <div className="bg-gradient-to-r from-cyan-50 to-blue-50 rounded-lg p-4 border border-cyan-100">
                  <p className="text-sm text-slate-700 leading-relaxed">
                    Traditional RAG retrieves chunks based on <span className="font-semibold">similarity</span> alone.
                    But real questions often require <span className="font-semibold">multi-hop reasoning</span>:
                  </p>
                  <div className="mt-3 bg-white rounded-lg p-3 border text-sm">
                    <p className="font-medium text-slate-800">"What parameters affect Runtime Filter performance?"</p>
                    <p className="text-slate-600 mt-2 flex items-center flex-wrap gap-1">
                      <span className="px-2 py-0.5 bg-cyan-100 text-cyan-700 rounded font-medium">Runtime Filter</span>
                      <ArrowRight className="w-4 h-4 text-slate-400" />
                      <span className="px-2 py-0.5 bg-purple-100 text-purple-700 rounded font-medium">bloom_filter_size</span>
                      <ArrowRight className="w-4 h-4 text-slate-400" />
                      <span className="px-2 py-0.5 bg-amber-100 text-amber-700 rounded font-medium">query performance</span>
                    </p>
                  </div>
                  <p className="text-xs text-slate-500 mt-3 italic">
                    Knowledge graphs capture entity relationships, enabling retrieval of contextually related information that vector search alone would miss.
                  </p>
                </div>
              </section>

              {/* Industry Approach - GraphRAG/LightRAG */}
              <section className="space-y-3">
                <h3 className="font-semibold text-lg flex items-center gap-2 text-purple-700">
                  <GitBranch className="w-5 h-5" />
                  Industry Approach: Precompute Communities
                </h3>
                <div className="bg-purple-50 rounded-lg p-4 border border-purple-100">
                  <div className="flex items-center gap-2 mb-3">
                    <span className="text-xs px-2 py-1 bg-purple-200 text-purple-800 rounded font-medium">Microsoft GraphRAG</span>
                    <span className="text-xs px-2 py-1 bg-purple-200 text-purple-800 rounded font-medium">LightRAG</span>
                  </div>
                  <img
                    src="/images/bytedance_knowledgegraph.png"
                    alt="Industry Knowledge Graph Architecture"
                    className="w-full rounded-lg border shadow-sm mb-4"
                  />
                  <div className="grid md:grid-cols-2 gap-4">
                    <div className="bg-white rounded-lg p-3 border">
                      <p className="font-semibold text-purple-700 mb-2 text-sm">Build Phase (Offline)</p>
                      <div className="space-y-2 text-xs">
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">1</span>
                          <span>Extract entities & relationships via LLM</span>
                        </div>
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">2</span>
                          <span>Cluster entities using <strong>Leiden algorithm</strong></span>
                        </div>
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">3</span>
                          <span>Generate <strong>community summaries</strong> with LLM</span>
                        </div>
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">4</span>
                          <span>Store in separate entity/community tables</span>
                        </div>
                      </div>
                    </div>
                    <div className="bg-white rounded-lg p-3 border">
                      <p className="font-semibold text-purple-700 mb-2 text-sm">Query Phase</p>
                      <div className="space-y-2 text-xs">
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">1</span>
                          <span>Vector search → seed entities</span>
                        </div>
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">2</span>
                          <span>Lookup community summaries</span>
                        </div>
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">3</span>
                          <span>Collect 1-hop neighbor entities</span>
                        </div>
                        <div className="flex gap-2">
                          <span className="bg-purple-100 text-purple-700 px-1.5 py-0.5 rounded font-mono">4</span>
                          <span>Combine context → LLM</span>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="mt-3 p-2 bg-purple-100 rounded text-xs text-purple-800">
                    <strong>Tradeoff:</strong> Requires batch reprocessing when data changes. Community summaries become stale.
                  </div>
                </div>
              </section>

              {/* Current Approach - Materialized Views */}
              <section className="space-y-3">
                <h3 className="font-semibold text-lg flex items-center gap-2 text-cyan-700">
                  <Database className="w-5 h-5" />
                  Current Approach: Materialized Views + Query-Time BFS
                </h3>
                <div className="bg-cyan-50 rounded-lg p-4 border border-cyan-100">
                  <p className="text-sm text-slate-700 mb-4">
                    Instead of precomputing communities, we use <strong>auto-refreshing Materialized Views</strong> and perform <strong>2-hop BFS at query time</strong>:
                  </p>

                  {/* Architecture Diagram */}
                  <div className="bg-white rounded-lg p-4 border mb-4">
                    <p className="text-xs font-semibold text-slate-600 mb-3">ARCHITECTURE</p>
                    <div className="flex items-center justify-between gap-2 text-xs overflow-x-auto">
                      <div className="flex flex-col items-center min-w-[100px]">
                        <div className="w-16 h-16 rounded-lg bg-blue-100 border-2 border-blue-300 flex items-center justify-center">
                          <FileText className="w-6 h-6 text-blue-600" />
                        </div>
                        <span className="mt-1 font-medium">rag_unified</span>
                        <span className="text-slate-500">base table</span>
                      </div>
                      <ArrowRight className="w-5 h-5 text-slate-400 flex-shrink-0" />
                      <div className="flex flex-col items-center min-w-[100px]">
                        <div className="w-16 h-16 rounded-lg bg-amber-100 border-2 border-amber-300 flex items-center justify-center text-center text-[10px] font-medium p-1">
                          mv_entity_index
                        </div>
                        <span className="mt-1 font-medium">Entity→Chunks</span>
                        <span className="text-slate-500">auto-refresh</span>
                      </div>
                      <ArrowRight className="w-5 h-5 text-slate-400 flex-shrink-0" />
                      <div className="flex flex-col items-center min-w-[100px]">
                        <div className="w-16 h-16 rounded-lg bg-purple-100 border-2 border-purple-300 flex items-center justify-center text-center text-[10px] font-medium p-1">
                          mv_triple_index
                        </div>
                        <span className="mt-1 font-medium">Triples</span>
                        <span className="text-slate-500">auto-refresh</span>
                      </div>
                      <ArrowRight className="w-5 h-5 text-slate-400 flex-shrink-0" />
                      <div className="flex flex-col items-center min-w-[100px]">
                        <div className="w-16 h-16 rounded-lg bg-green-100 border-2 border-green-300 flex items-center justify-center">
                          <Zap className="w-6 h-6 text-green-600" />
                        </div>
                        <span className="mt-1 font-medium">2-Hop BFS</span>
                        <span className="text-slate-500">query time</span>
                      </div>
                    </div>
                  </div>

                  {/* MV Definitions */}
                  <div className="space-y-3">
                    <div className="bg-slate-900 rounded-lg p-3 text-xs font-mono overflow-x-auto">
                      <div className="text-slate-400">-- MV 1: Entity → Chunk mapping (auto-refreshes ON COMMIT)</div>
                      <div className="text-cyan-400 mt-1">CREATE MATERIALIZED VIEW <span className="text-white">mv_entity_index</span></div>
                      <div className="text-green-400">REFRESH AUTO ON COMMIT</div>
                      <div className="text-slate-300">AS SELECT entity_name, <span className="text-amber-400">COLLECT_SET(row_id)</span> as chunk_ids</div>
                      <div className="text-slate-300">   FROM rag_unified <span className="text-purple-400">LATERAL VIEW EXPLODE(entity_names)</span> t</div>
                      <div className="text-slate-300">   GROUP BY entity_name;</div>
                    </div>

                    <div className="bg-slate-900 rounded-lg p-3 text-xs font-mono overflow-x-auto">
                      <div className="text-slate-400">-- MV 2: Triple index for relationship traversal</div>
                      <div className="text-cyan-400 mt-1">CREATE MATERIALIZED VIEW <span className="text-white">mv_triple_index</span></div>
                      <div className="text-green-400">REFRESH AUTO ON COMMIT</div>
                      <div className="text-slate-300">AS SELECT</div>
                      <div className="text-slate-300">   <span className="text-amber-400">JSON_EXTRACT_STRING</span>(rel, '$.source') as source_entity,</div>
                      <div className="text-slate-300">   <span className="text-amber-400">JSON_EXTRACT_STRING</span>(rel, '$.target') as target_entity,</div>
                      <div className="text-slate-300">   row_id as chunk_id</div>
                      <div className="text-slate-300">FROM rag_unified LATERAL VIEW EXPLODE(relationships) t AS rel;</div>
                    </div>
                  </div>

                  {/* 2-Hop BFS Query */}
                  <div className="mt-4 bg-white rounded-lg p-3 border">
                    <p className="text-xs font-semibold text-cyan-700 mb-2">2-HOP BFS QUERY (at query time)</p>
                    <div className="bg-slate-900 rounded-lg p-3 text-xs font-mono overflow-x-auto">
                      <div className="text-cyan-400">WITH <span className="text-amber-400">hop1</span> AS (</div>
                      <div className="text-slate-300 pl-2">SELECT target_entity, chunk_id FROM mv_triple_index</div>
                      <div className="text-slate-300 pl-2">WHERE source_entity = <span className="text-green-400">'Runtime Filter'</span>  <span className="text-slate-500">-- seed entity</span></div>
                      <div className="text-cyan-400">),</div>
                      <div className="text-cyan-400"><span className="text-purple-400">hop2</span> AS (</div>
                      <div className="text-slate-300 pl-2">SELECT t.target_entity, t.chunk_id FROM mv_triple_index t</div>
                      <div className="text-slate-300 pl-2">JOIN hop1 h ON t.source_entity = h.target_entity  <span className="text-slate-500">-- follow edges</span></div>
                      <div className="text-cyan-400">)</div>
                      <div className="text-slate-300">SELECT * FROM hop1 UNION SELECT * FROM hop2;</div>
                    </div>
                  </div>
                </div>
              </section>

              {/* Comparison Table */}
              <section className="space-y-3">
                <h3 className="font-semibold text-lg flex items-center gap-2 text-slate-700">
                  <CheckCircle2 className="w-5 h-5" />
                  Comparison: Precompute vs Query-Time
                </h3>
                <div className="overflow-x-auto">
                  <table className="w-full text-xs border-collapse">
                    <thead>
                      <tr className="bg-slate-100">
                        <th className="border border-slate-300 px-3 py-2 text-left font-semibold">Aspect</th>
                        <th className="border border-slate-300 px-3 py-2 text-left font-semibold text-purple-700">Precompute (GraphRAG/LightRAG)</th>
                        <th className="border border-slate-300 px-3 py-2 text-left font-semibold text-cyan-700">Current (MV + BFS)</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td className="border border-slate-300 px-3 py-2 font-medium">Data Freshness</td>
                        <td className="border border-slate-300 px-3 py-2 text-red-600">Stale until reindex</td>
                        <td className="border border-slate-300 px-3 py-2 text-green-600">Always fresh (ON COMMIT)</td>
                      </tr>
                      <tr>
                        <td className="border border-slate-300 px-3 py-2 font-medium">Indexing Cost</td>
                        <td className="border border-slate-300 px-3 py-2">High (Leiden + LLM summaries)</td>
                        <td className="border border-slate-300 px-3 py-2">Low (MV auto-refresh)</td>
                      </tr>
                      <tr>
                        <td className="border border-slate-300 px-3 py-2 font-medium">Query Latency</td>
                        <td className="border border-slate-300 px-3 py-2">~50ms (lookup)</td>
                        <td className="border border-slate-300 px-3 py-2">~100-300ms (BFS joins)</td>
                      </tr>
                      <tr>
                        <td className="border border-slate-300 px-3 py-2 font-medium">Global Queries</td>
                        <td className="border border-slate-300 px-3 py-2 text-green-600">Good (community summaries)</td>
                        <td className="border border-slate-300 px-3 py-2 text-amber-600">Limited (no summaries)</td>
                      </tr>
                      <tr>
                        <td className="border border-slate-300 px-3 py-2 font-medium">Infrastructure</td>
                        <td className="border border-slate-300 px-3 py-2">Multiple tables + batch jobs</td>
                        <td className="border border-slate-300 px-3 py-2 text-green-600">Single table + MVs</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </section>

              {/* Key Benefits */}
              <section className="space-y-3">
                <h3 className="font-semibold text-lg flex items-center gap-2 text-emerald-700">
                  <Zap className="w-5 h-5" />
                  Why This Approach Works for VeloDB
                </h3>
                <div className="grid md:grid-cols-2 gap-3">
                  {[
                    {
                      title: "Always Fresh",
                      desc: "MVs auto-refresh ON COMMIT — no stale community summaries",
                      icon: "🔄",
                    },
                    {
                      title: "No Batch Jobs",
                      desc: "No Leiden clustering or LLM summarization pipelines to maintain",
                      icon: "🎯",
                    },
                    {
                      title: "SQL-Native",
                      desc: "2-hop BFS via standard SQL JOINs — no graph DB required",
                      icon: "💾",
                    },
                    {
                      title: "Explainable",
                      desc: "Full traversal path visible in query results",
                      icon: "🔍",
                    },
                  ].map((benefit, i) => (
                    <div key={i} className="bg-emerald-50 rounded-lg p-3 border border-emerald-100">
                      <div className="flex items-center gap-2 mb-1">
                        <span className="text-lg">{benefit.icon}</span>
                        <span className="font-semibold text-sm text-emerald-800">{benefit.title}</span>
                      </div>
                      <p className="text-xs text-slate-600">{benefit.desc}</p>
                    </div>
                  ))}
                </div>
              </section>

              {/* Sources */}
              <section className="text-xs text-slate-500 border-t pt-3">
                <p className="font-medium mb-1">References:</p>
                <ul className="space-y-1">
                  <li>• <a href="https://microsoft.github.io/graphrag/" target="_blank" rel="noopener noreferrer" className="text-cyan-600 hover:underline">Microsoft GraphRAG</a> - Community detection with Leiden algorithm</li>
                  <li>• <a href="https://github.com/HKUDS/LightRAG" target="_blank" rel="noopener noreferrer" className="text-cyan-600 hover:underline">LightRAG (EMNLP 2025)</a> - Dual-level entity retrieval</li>
                  <li>• <a href="https://arxiv.org/abs/2408.08921" target="_blank" rel="noopener noreferrer" className="text-cyan-600 hover:underline">Graph RAG Survey</a> - Comprehensive overview</li>
                </ul>
              </section>

            </div>
          </div>
        </DialogContent>
      </Dialog>

    </Card>
  );
};

export default KnowledgeGraphPanel;
