import { useState, useMemo } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  HelpCircle,
  Check,
  Loader2,
  Code,
  FileText,
  Sparkles,
  Share2,
  ChevronDown,
  ChevronUp,
  Eye,
  GitBranch,
  Tag,
  ArrowRight,
} from "lucide-react";
import { cn } from "@/lib/utils";

export type ContextStepType = "system" | "keyword" | "semantic" | "graph";

// Chunk data for preview
export interface ChunkPreview {
  chunk_id: string;
  content: string;
  doc_id: string;
  score?: number;
  entity_names?: string[];
  relationships?: Array<{
    source: string;
    relation: string;
    target: string;
  }>;
}

interface ContextStepConfig {
  id: ContextStepType;
  name: string;
  icon: React.ReactNode;
  color: string;
  bgColor: string;
  borderColor: string;
  explanation: string;
  velodbFeature: string;
  sql: string;
}

const stepConfigs: Record<ContextStepType, ContextStepConfig> = {
  system: {
    id: "system",
    name: "System Prompt",
    icon: <Code className="w-4 h-4" />,
    color: "text-blue-600",
    bgColor: "bg-blue-50",
    borderColor: "border-blue-300",
    explanation: "Defines how the AI should behave, what to cite, and response format.",
    velodbFeature: "Stored in rag_unified as content_type='system'",
    sql: `SELECT content FROM rag_unified
WHERE content_type = 'system'
  AND tenant_id = ?`,
  },
  keyword: {
    id: "keyword",
    name: "Keyword Search",
    icon: <FileText className="w-4 h-4" />,
    color: "text-amber-600",
    bgColor: "bg-amber-50",
    borderColor: "border-amber-300",
    explanation: "Finds chunks containing exact query terms using inverted index.",
    velodbFeature: "INVERTED INDEX on content column with MATCH operator",
    sql: `SELECT content, score()
FROM rag_unified
WHERE content MATCH 'vector search'
ORDER BY score() DESC
LIMIT 5`,
  },
  semantic: {
    id: "semantic",
    name: "Semantic Search",
    icon: <Sparkles className="w-4 h-4" />,
    color: "text-purple-600",
    bgColor: "bg-purple-50",
    borderColor: "border-purple-300",
    explanation: "Finds chunks with similar meaning using vector embeddings (BGE-M3, 1024 dims).",
    velodbFeature: "ANN INDEX (IVF) on content_embedding with l2_distance",
    sql: `SELECT content, l2_distance(content_embedding, ?) as dist
FROM rag_unified
ORDER BY dist ASC
LIMIT 5`,
  },
  graph: {
    id: "graph",
    name: "Knowledge Graph",
    icon: <Share2 className="w-4 h-4" />,
    color: "text-cyan-600",
    bgColor: "bg-cyan-50",
    borderColor: "border-cyan-300",
    explanation: "Finds related entities and their connections via Materialized View joins.",
    velodbFeature: "ARRAY INVERTED INDEX + mv_entity_chunks MV (auto-refresh)",
    sql: `-- 1-hop: Direct entity match
SELECT * FROM rag_unified
WHERE array_contains(entity_names, 'VeloDB')

-- 2-hop: Related entities via MV
WITH hop1 AS (
  SELECT chunk_ids FROM mv_entity_chunks
  WHERE entity_name = 'VeloDB'
)
SELECT * FROM mv_entity_chunks
WHERE ARRAY_INTERSECT(chunk_ids, hop1.chunk_ids) > 0`,
  },
};

// Graph edge type for real entity→relation→entity relationships
export interface GraphEdge {
  source: string;
  target: string;
  relation: string;
}

interface ContextStepCardProps {
  step: ContextStepType;
  status: "pending" | "loading" | "complete";
  tokens?: number;
  maxTokens?: number;
  summary?: string;
  details?: string[];
  timeMs?: number;
  mermaid?: string;
  chunks?: ChunkPreview[];
  systemPromptContent?: string;
  className?: string;
  // Graph-specific props
  graphEdges?: GraphEdge[];
  seedEntities?: string[];
  query?: string; // Original search query for graph visualization
}

const ContextStepCard = ({
  step,
  status,
  tokens = 0,
  maxTokens = 2000,
  summary,
  details = [],
  timeMs = 0,
  mermaid,
  chunks = [],
  systemPromptContent,
  className,
  graphEdges = [],
  seedEntities = [],
  query = "",
}: ContextStepCardProps) => {
  const [showGraph, setShowGraph] = useState(false);
  const [showChunks, setShowChunks] = useState(false);
  const [expandedChunk, setExpandedChunk] = useState<string | null>(null);
  const config = stepConfigs[step];
  const tokenPercent = Math.min((tokens / maxTokens) * 100, 100);

  return (
    <Card
      className={cn(
        "transition-all duration-200",
        status === "pending" && "opacity-40",
        status === "loading" && "ring-2 animate-pulse",
        status === "loading" && config.borderColor.replace("border-", "ring-"),
        status === "complete" && config.borderColor,
        status === "complete" && "border-l-4",
        className
      )}
    >
      <CardContent className="p-2 sm:p-3">
        <div className="flex items-start gap-2 sm:gap-3">
          {/* Status Icon */}
          <div
            className={cn(
              "p-1.5 sm:p-2 rounded-lg shrink-0",
              status === "complete" ? config.bgColor : "bg-gray-100"
            )}
          >
            {status === "loading" ? (
              <Loader2 className={cn("w-4 h-4 animate-spin", config.color)} />
            ) : status === "complete" ? (
              <div className={config.color}>{config.icon}</div>
            ) : (
              <div className="text-gray-400">{config.icon}</div>
            )}
          </div>

          {/* Content */}
          <div className="flex-1 min-w-0">
            {/* Header */}
            <div className="flex items-center justify-between gap-1 sm:gap-2 flex-wrap">
              <div className="flex items-center gap-1.5">
                <span className={cn("font-semibold text-sm", config.color)}>
                  {config.name}
                </span>
                {status === "complete" && (
                  <Check className="w-3.5 h-3.5 text-green-500" />
                )}
              </div>

              <div className="flex items-center gap-1.5 sm:gap-2">
                {/* Timing */}
                {status === "complete" && timeMs > 0 && (
                  <span className="text-[10px] sm:text-xs font-mono text-green-600 bg-green-50 px-1 sm:px-1.5 py-0.5 rounded">
                    {timeMs}ms
                  </span>
                )}
                {/* Token count */}
                {status === "complete" && tokens > 0 && (
                  <span className="text-[10px] sm:text-xs font-mono text-muted-foreground">
                    {tokens} tok
                  </span>
                )}

                {/* Help button */}
                <Dialog>
                  <DialogTrigger asChild>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 w-6 p-0 hover:bg-gray-100"
                    >
                      <HelpCircle className="w-4 h-4 text-muted-foreground" />
                    </Button>
                  </DialogTrigger>
                  <DialogContent className="max-w-lg">
                    <DialogHeader>
                      <DialogTitle className="flex items-center gap-2">
                        <div className={cn("p-2 rounded-lg", config.bgColor)}>
                          <div className={config.color}>{config.icon}</div>
                        </div>
                        How {config.name} Works
                      </DialogTitle>
                    </DialogHeader>
                    <div className="space-y-4">
                      {/* Explanation */}
                      <div>
                        <p className="text-sm text-muted-foreground">
                          {config.explanation}
                        </p>
                      </div>

                      {/* VeloDB Feature */}
                      <div className={cn("p-3 rounded-lg", config.bgColor)}>
                        <div className="text-xs font-medium mb-1">
                          VeloDB Feature
                        </div>
                        <p className={cn("text-sm font-mono", config.color)}>
                          {config.velodbFeature}
                        </p>
                      </div>

                      {/* SQL */}
                      <div className="bg-slate-900 rounded-lg p-3">
                        <div className="text-xs font-medium text-slate-400 mb-2">
                          SQL Query
                        </div>
                        <pre className="text-xs text-slate-300 font-mono whitespace-pre-wrap overflow-x-auto">
                          {config.sql}
                        </pre>
                      </div>
                    </div>
                  </DialogContent>
                </Dialog>
              </div>
            </div>

            {/* Summary */}
            {status === "complete" && summary && (
              <p className="text-xs text-muted-foreground mt-1 truncate">
                {summary}
              </p>
            )}

            {/* Progress bar */}
            {status === "complete" && tokens > 0 && (
              <div className="mt-2 h-1.5 bg-gray-100 rounded-full overflow-hidden">
                <div
                  className={cn(
                    "h-full transition-all duration-500",
                    config.bgColor.replace("bg-", "bg-").replace("-50", "-400")
                  )}
                  style={{ width: `${tokenPercent}%` }}
                />
              </div>
            )}

            {/* Details list */}
            {status === "complete" && details.length > 0 && (
              <div className="mt-2 space-y-1">
                {details.slice(0, 3).map((detail, i) => (
                  <div
                    key={i}
                    className="text-xs text-muted-foreground flex items-center gap-1"
                  >
                    <span className={cn("w-1.5 h-1.5 rounded-full", config.bgColor.replace("-50", "-400"))} />
                    <span className="truncate">{detail}</span>
                  </div>
                ))}
              </div>
            )}

            {/* System Prompt Content (for system step) */}
            {status === "complete" && step === "system" && systemPromptContent && (
              <Collapsible open={showChunks} onOpenChange={setShowChunks} className="mt-3">
                <CollapsibleTrigger asChild>
                  <button
                    className={cn(
                      "flex items-center gap-1 text-xs font-medium px-2 py-1 rounded transition-colors w-full justify-between",
                      showChunks ? "bg-blue-100 text-blue-700" : "bg-gray-100 text-gray-600 hover:bg-gray-200"
                    )}
                  >
                    <span className="flex items-center gap-1">
                      <Eye className="w-3 h-3" />
                      View System Prompt
                    </span>
                    {showChunks ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
                  </button>
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <div className="mt-2 p-3 bg-blue-50 border border-blue-200 rounded-lg">
                    <pre className="text-xs text-blue-800 font-mono whitespace-pre-wrap overflow-x-auto max-h-40 overflow-y-auto">
                      {systemPromptContent}
                    </pre>
                  </div>
                </CollapsibleContent>
              </Collapsible>
            )}

            {/* Chunk Previews (for search steps) */}
            {status === "complete" && chunks.length > 0 && step !== "system" && (
              <Collapsible open={showChunks} onOpenChange={setShowChunks} className="mt-3">
                <CollapsibleTrigger asChild>
                  <button
                    className={cn(
                      "flex items-center gap-1 text-xs font-medium px-2 py-1 rounded transition-colors w-full justify-between",
                      showChunks ? config.bgColor + " " + config.color : "bg-gray-100 text-gray-600 hover:bg-gray-200"
                    )}
                  >
                    <span className="flex items-center gap-1">
                      <Eye className="w-3 h-3" />
                      View {chunks.length} Chunks
                    </span>
                    {showChunks ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
                  </button>
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <div className="mt-2 space-y-2 max-h-60 overflow-y-auto pr-1">
                    {chunks.map((chunk, idx) => (
                      <div
                        key={chunk.chunk_id}
                        className={cn(
                          "p-2 rounded-lg border cursor-pointer transition-all",
                          config.bgColor,
                          config.borderColor,
                          expandedChunk === chunk.chunk_id && "ring-2 ring-offset-1",
                          expandedChunk === chunk.chunk_id && config.borderColor.replace("border-", "ring-")
                        )}
                        onClick={() => setExpandedChunk(
                          expandedChunk === chunk.chunk_id ? null : chunk.chunk_id
                        )}
                      >
                        {/* Chunk Header */}
                        <div className="flex items-center justify-between gap-2 mb-1">
                          <div className="flex items-center gap-2">
                            <span className={cn(
                              "text-xs font-bold px-1.5 py-0.5 rounded",
                              config.bgColor.replace("-50", "-200"),
                              config.color
                            )}>
                              #{idx + 1}
                            </span>
                            <span className="text-xs text-muted-foreground truncate max-w-[120px]">
                              {chunk.doc_id}
                            </span>
                          </div>
                          {chunk.score !== undefined && (
                            <span className={cn(
                              "text-xs font-mono px-1.5 py-0.5 rounded",
                              config.bgColor.replace("-50", "-100"),
                              config.color
                            )}>
                              {step === "semantic" ? `dist: ${chunk.score.toFixed(3)}` : `score: ${chunk.score.toFixed(2)}`}
                            </span>
                          )}
                        </div>

                        {/* Chunk Content Preview */}
                        <p className={cn(
                          "text-xs text-foreground",
                          expandedChunk === chunk.chunk_id ? "whitespace-pre-wrap" : "line-clamp-2"
                        )}>
                          {chunk.content}
                        </p>

                        {/* Entity tags (if available) */}
                        {expandedChunk === chunk.chunk_id && chunk.entity_names && chunk.entity_names.length > 0 && (
                          <div className="mt-2 flex flex-wrap gap-1">
                            {chunk.entity_names.slice(0, 5).map((entity, i) => (
                              <span
                                key={i}
                                className="text-[10px] px-1.5 py-0.5 bg-cyan-100 text-cyan-700 rounded"
                              >
                                {entity}
                              </span>
                            ))}
                            {chunk.entity_names.length > 5 && (
                              <span className="text-[10px] text-muted-foreground">
                                +{chunk.entity_names.length - 5} more
                              </span>
                            )}
                          </div>
                        )}

                        {/* Expand hint */}
                        {expandedChunk !== chunk.chunk_id && chunk.content.length > 100 && (
                          <p className="text-[10px] text-muted-foreground mt-1">
                            Click to expand...
                          </p>
                        )}
                      </div>
                    ))}
                  </div>
                </CollapsibleContent>
              </Collapsible>
            )}

            {/* Entity Traversal View (only for graph step) */}
            {status === "complete" && step === "graph" && chunks.length > 0 && (
              <div className="mt-3">
                <button
                  onClick={() => setShowGraph(!showGraph)}
                  className={cn(
                    "flex items-center gap-1 text-xs font-medium px-2 py-1 rounded transition-colors w-full justify-between",
                    showGraph ? "bg-cyan-100 text-cyan-700" : "bg-gray-100 text-gray-600 hover:bg-gray-200"
                  )}
                >
                  <span className="flex items-center gap-1">
                    <GitBranch className="w-3 h-3" />
                    Entity Traversal
                  </span>
                  {showGraph ? <ChevronUp className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
                </button>

                {showGraph && (
                  <div className="mt-2 rounded-lg overflow-hidden">
                    <EntityTraversalView
                      query={query}
                      chunks={chunks}
                      graphEdges={graphEdges}
                      seedEntities={seedEntities}
                    />
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
};

// Format relation name for display
const formatRelation = (relation: string): string => {
  return relation
    .replace(/_/g, " ")
    .toLowerCase();
};

// Flow path type for horizontal visualization
interface FlowPath {
  seed: string;
  relation: string;
  hopEntity: string;
  chunkBriefs: Array<{ idx: number; brief: string }>;
}

// K-hop Traversal View - Horizontal flow: Query → Seed → [relation] → Hop → Chunks
const EntityTraversalView = ({
  query = "",
  chunks,
  graphEdges = [],
  seedEntities = [],
}: {
  query?: string;
  chunks: ChunkPreview[];
  graphEdges?: GraphEdge[];
  seedEntities?: string[];
}) => {
  // Build entity → chunk mapping (which entities appear in which chunks)
  const entityToChunks = useMemo(() => {
    const map = new Map<string, number[]>(); // entity -> chunk indices
    chunks.forEach((chunk, idx) => {
      (chunk.entity_names || []).forEach(entity => {
        const key = entity.toLowerCase();
        if (!map.has(key)) {
          map.set(key, []);
        }
        map.get(key)!.push(idx + 1); // 1-indexed for display
      });
    });
    return map;
  }, [chunks]);

  // Build flow paths from edges with chunk briefs
  const flowPaths = useMemo(() => {
    const paths: FlowPath[] = [];
    const seedSet = new Set(seedEntities.map(s => s.toLowerCase()));

    graphEdges.forEach(edge => {
      // Determine which is seed vs hop entity
      const isSeedSource = seedSet.has(edge.source.toLowerCase());
      const seed = isSeedSource ? edge.source : edge.target;
      const hopEntity = isSeedSource ? edge.target : edge.source;

      // Get chunks where hop entity appears
      const chunkIndices = entityToChunks.get(hopEntity.toLowerCase()) || [];
      const chunkBriefs = chunkIndices.slice(0, 2).map(idx => ({
        idx,
        brief: chunks[idx - 1]?.content.slice(0, 35).replace(/\n/g, " ") || ""
      }));

      paths.push({ seed, relation: edge.relation, hopEntity, chunkBriefs });
    });

    return paths;
  }, [graphEdges, seedEntities, entityToChunks, chunks]);

  // If no data at all
  if (seedEntities.length === 0 && chunks.length === 0) {
    return (
      <div className="flex items-center justify-center h-20 bg-cyan-50 border border-cyan-200 rounded-lg text-slate-500 text-xs">
        No graph data available
      </div>
    );
  }

  const hasFlowPaths = flowPaths.length > 0;

  return (
    <div className="bg-cyan-50 border border-cyan-200 rounded-lg p-3 space-y-2 max-h-72 overflow-y-auto font-mono text-xs">
      {/* Query at top */}
      {query && (
        <div className="text-slate-600 mb-2">
          Query: <span className="text-cyan-800 font-semibold">"{query}"</span>
        </div>
      )}

      {/* Flow paths: Seed → [relation] → Hop → Chunks */}
      {hasFlowPaths ? (
        <div className="space-y-2">
          {flowPaths.slice(0, 6).map((path, idx) => (
            <div key={idx} className="space-y-0.5">
              {/* Main flow line */}
              <div className="flex items-center gap-1.5 flex-wrap">
                <span className="px-2 py-0.5 bg-emerald-100 text-emerald-700 rounded border border-emerald-300">
                  {path.seed}
                </span>
                <span className="text-slate-400">──[</span>
                <span className="text-amber-700 font-medium">{formatRelation(path.relation)}</span>
                <span className="text-slate-400">]──→</span>
                <span className="px-2 py-0.5 bg-teal-100 text-teal-700 rounded border border-teal-300">
                  {path.hopEntity}
                </span>
                {path.chunkBriefs.length > 0 && (
                  <span className="text-slate-400">──→</span>
                )}
              </div>

              {/* Chunk briefs (indented under the flow) */}
              {path.chunkBriefs.map(({ idx: chunkIdx, brief }) => (
                <div key={chunkIdx} className="ml-8 flex items-center gap-1.5 text-slate-600">
                  <span className="text-cyan-700 font-semibold">[{chunkIdx}]</span>
                  <span className="truncate text-slate-500">"{brief}..."</span>
                </div>
              ))}
            </div>
          ))}
          {flowPaths.length > 6 && (
            <div className="text-slate-500 text-center">... +{flowPaths.length - 6} more paths</div>
          )}
        </div>
      ) : (
        /* Fallback: Show seeds → chunks directly when no edges */
        <div className="space-y-1">
          {seedEntities.slice(0, 4).map((entity) => {
            const chunkNums = entityToChunks.get(entity.toLowerCase()) || [];
            return (
              <div key={entity} className="flex items-center gap-2 flex-wrap">
                <span className="px-2 py-0.5 bg-emerald-100 text-emerald-700 rounded border border-emerald-300">
                  {entity}
                </span>
                {chunkNums.length > 0 && (
                  <>
                    <span className="text-slate-400">──→</span>
                    <div className="flex gap-1 flex-wrap">
                      {chunkNums.slice(0, 3).map(num => {
                        const brief = chunks[num - 1]?.content.slice(0, 25).replace(/\n/g, " ") || "";
                        return (
                          <span key={num} className="px-1.5 py-0.5 bg-cyan-100 text-cyan-700 rounded text-[10px]">
                            [{num}] "{brief}..."
                          </span>
                        );
                      })}
                      {chunkNums.length > 3 && (
                        <span className="text-slate-500 text-[10px]">+{chunkNums.length - 3}</span>
                      )}
                    </div>
                  </>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* Summary line */}
      <div className="pt-2 border-t border-cyan-200 text-slate-500 text-[10px] text-center">
        {seedEntities.length} seeds
        {hasFlowPaths ? ` → ${graphEdges.length} edges` : ""}
        {" → "}{chunks.length} chunks
      </div>
    </div>
  );
};

export default ContextStepCard;
export { stepConfigs };
export type { ChunkPreview, GraphEdge };
