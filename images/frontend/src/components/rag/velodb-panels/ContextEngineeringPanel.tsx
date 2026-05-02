import { useState, useCallback, useRef, Fragment, useMemo, useEffect } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Database,
  Play,
  Loader2,
  Sparkles,
  ArrowDown,
  MessageSquare,
  Image as ImageIcon,
  RotateCcw,
  FileText,
  Table2,
  Calculator,
  Code,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  GitBranch,
} from "lucide-react";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn, getAssetUrl, MultimodalData, RAG_API_BASE_URL as API_BASE_URL } from "@/lib/utils";
import ContextStepCard, { ContextStepType, ChunkPreview } from "@/components/rag/ContextStepCard";

// Demo queries showcasing different retrieval methods - matched to actual corpus content
const DEMO_QUERIES = [
  "How to filter slow SQL queries?",        // Keyword: exact term "slow query filtering"
  "optimize join performance in Doris",     // Semantic: conceptual query about joins
  "What is hash join and bucket shuffle?",  // Graph: entity relationships
];

// Default system prompt (matches backend)
const SYSTEM_PROMPT = `You are a VeloDB/Apache Doris documentation assistant.
Answer ONLY based on the provided context.

ACCURACY RULES:
- ONLY state facts EXPLICITLY written in the context
- If context doesn't have the answer, say so
- DO NOT add information not in context
- Quote technical terms exactly as they appear

CITATION FORMAT:
- Text: [1], [2], [3]
- Images: [IMG-1], [IMG-2]
- Tables: [TABLE-1], [TABLE-2]`;

// Pre-compiled noise filter for entity names (module-level for performance)
const ENTITY_NOISE_PATTERN = /^[a-z][0-9]?$|^\d|^[A-Z]{1,2}$|rc\d|coupled mode|\(ms\)|version|^(the|and|for|with)$/i;

interface ChunkResult {
  chunk_id: string;
  content: string;
  content_type: string;
  doc_id: string;
  score: number;
  entity_names?: string[];
  entities?: Array<{ name: string; type: string }>;
  relationships?: Array<{ source: string; relation: string; target: string }>;
  multimodal_data?: MultimodalData;
}

interface GraphEdge {
  source: string;
  target: string;
  relation: string;
}

interface SubgraphResult {
  nodes: { id: string; type: string; name?: string }[];
  edges: GraphEdge[];
  chunks: ChunkResult[];
  seed_entities: string[];
  retrieval_time_ms: number;
  mermaid?: string;
}

interface StepResult {
  status: "pending" | "loading" | "complete" | "error";
  tokens: number;
  timeMs: number;
  summary: string;
  details: string[];
  chunks?: ChunkResult[];
  error?: string;
  mermaid?: string;
  // Graph-specific data
  graphEdges?: GraphEdge[];
  seedEntities?: string[];
}

interface CitationItem {
  ref_id: string;
  type: "text" | "image" | "table" | "formula";
  chunk_id: string;
  original_markdown: string;
  text_preview?: string;
  image_path?: string;
  image_caption?: string;
  table_markdown?: string;
  latex?: string;
  score: number;
  document_name?: string;
  multimodal_data?: MultimodalData;
}

interface ContextEngineeringPanelProps {
  onSearch?: (query: string) => Promise<unknown>;
  tenantId?: string;
  corpusId?: string;
}

// Estimate token count from text (rough approximation: 1 token ≈ 4 chars)
const estimateTokens = (text: string): number => Math.ceil(text.length / 4);

// Render text with 【keyword】 highlights
const renderWithHighlights = (text: string): React.ReactNode => {
  const parts = text.split(/(【[^】]+】)/g);
  return parts.map((part, i) => {
    if (part.startsWith("【") && part.endsWith("】")) {
      const keyword = part.slice(1, -1);
      return (
        <mark key={i} className="bg-amber-300 text-amber-900 px-0.5 rounded font-semibold">
          {keyword}
        </mark>
      );
    }
    return part;
  });
};

// Citation type colors and icons
const citationConfig: Record<string, { color: string; bgColor: string; icon: typeof FileText }> = {
  text: { color: "text-blue-700", bgColor: "bg-blue-100", icon: FileText },
  image: { color: "text-purple-700", bgColor: "bg-purple-100", icon: ImageIcon },
  table: { color: "text-green-700", bgColor: "bg-green-100", icon: Table2 },
  formula: { color: "text-orange-700", bgColor: "bg-orange-100", icon: Calculator },
};

// Citation badge component - clickable with popover
const CitationBadge = ({
  refId,
  citation,
  tenantId,
  corpusId,
}: {
  refId: string;
  citation: CitationItem | undefined;
  tenantId?: string;
  corpusId?: string;
}) => {
  if (!citation) return <span className="text-muted-foreground">{refId}</span>;
  const config = citationConfig[citation.type] || citationConfig.text;
  const Icon = config.icon;

  // Get image URL for image citations
  const imageUrl = citation.type === "image" && citation.multimodal_data
    ? getAssetUrl(citation.multimodal_data, tenantId || "", corpusId || "")
    : null;

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          className={cn(
            "inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-xs font-semibold mx-0.5 cursor-pointer hover:ring-2 hover:ring-offset-1 transition-all",
            config.bgColor,
            config.color
          )}
        >
          <Icon className="w-3 h-3" />
          {refId}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-96 p-0" align="start">
        <div className={cn("px-3 py-2 border-b flex items-center gap-2", config.bgColor)}>
          <Icon className={cn("w-4 h-4", config.color)} />
          <span className={cn("font-semibold text-sm", config.color)}>{refId}</span>
          <span className="text-xs text-muted-foreground ml-auto truncate max-w-[150px]">
            {citation.document_name}
          </span>
        </div>
        <ScrollArea className="max-h-[300px]">
          <div className="p-3">
            {citation.type === "image" && imageUrl ? (
              <div className="space-y-2">
                <img
                  src={imageUrl}
                  alt={citation.image_caption || "Image"}
                  className="w-full rounded border object-contain max-h-[200px] bg-slate-50"
                />
                {citation.image_caption && (
                  <p className="text-xs text-muted-foreground italic">{citation.image_caption}</p>
                )}
              </div>
            ) : citation.type === "table" && citation.table_markdown ? (
              <div className="prose prose-sm max-w-none overflow-x-auto">
                <ReactMarkdown
                  remarkPlugins={[remarkGfm]}
                  components={{
                    table: ({ children }) => (
                      <table className="text-xs border-collapse w-full">{children}</table>
                    ),
                    th: ({ children }) => (
                      <th className="border border-slate-300 bg-slate-100 px-2 py-1 text-left font-semibold">{children}</th>
                    ),
                    td: ({ children }) => (
                      <td className="border border-slate-300 px-2 py-1">{children}</td>
                    ),
                  }}
                >
                  {citation.table_markdown}
                </ReactMarkdown>
              </div>
            ) : citation.type === "formula" && citation.latex ? (
              <div className="text-sm font-mono bg-slate-50 p-2 rounded">
                {citation.latex}
              </div>
            ) : (
              <div className="prose prose-sm max-w-none">
                <ReactMarkdown
                  remarkPlugins={[remarkGfm]}
                  components={{
                    code: ({ className, children }) => {
                      const isInline = !className;
                      return isInline ? (
                        <code className="bg-slate-100 px-1 py-0.5 rounded text-xs font-mono">{children}</code>
                      ) : (
                        <pre className="bg-slate-900 text-slate-100 p-3 rounded-lg overflow-x-auto text-xs">
                          <code>{children}</code>
                        </pre>
                      );
                    },
                    pre: ({ children }) => <>{children}</>,
                  }}
                >
                  {citation.text_preview || citation.original_markdown?.slice(0, 500) || "No content available"}
                </ReactMarkdown>
              </div>
            )}
          </div>
        </ScrollArea>
      </PopoverContent>
    </Popover>
  );
};

// Render markdown with inline citations
const MarkdownWithCitations = ({
  content,
  citations,
  tenantId,
  corpusId,
}: {
  content: string;
  citations: CitationItem[];
  tenantId?: string;
  corpusId?: string;
}) => {
  const citationMap = new Map<string, CitationItem>();
  citations.forEach((c) => {
    const key = c.ref_id.replace(/^\[|\]$/g, "");
    citationMap.set(key, c);
  });

  const processText = (text: string): React.ReactNode[] => {
    const parts: React.ReactNode[] = [];
    const citationPattern = /\[((?:\d+)|(?:IMG-\d+)|(?:TABLE-\d+)|(?:EQ-\d+))\]/g;

    let lastIndex = 0;
    let match;

    while ((match = citationPattern.exec(text)) !== null) {
      if (match.index > lastIndex) {
        parts.push(text.slice(lastIndex, match.index));
      }

      const refId = match[1];
      const citation = citationMap.get(refId);
      const displayRefId = `[${refId}]`;

      // For images, render inline
      if (citation && citation.type === "image" && (citation.image_path || citation.multimodal_data)) {
        const imageUrl = getAssetUrl(
          citation.multimodal_data || citation.image_path,
          tenantId || "",
          corpusId || ""
        );
        parts.push(
          <figure key={`img-${match.index}`} className="my-3 mx-auto max-w-[90%]">
            <div className="relative rounded-lg overflow-hidden bg-slate-50 p-2 border">
              <img
                src={imageUrl}
                alt={citation.image_caption || "Image"}
                className="w-full rounded object-contain max-h-[250px]"
              />
              <span className="absolute top-2 right-2 px-2 py-1 bg-purple-100 text-purple-700 text-xs rounded-full font-semibold">
                {displayRefId}
              </span>
            </div>
            {citation.image_caption && (
              <figcaption className="mt-1 text-center text-xs text-slate-500 italic">
                {citation.image_caption}
              </figcaption>
            )}
          </figure>
        );
      } else {
        parts.push(
          <CitationBadge
            key={`cite-${match.index}`}
            refId={displayRefId}
            citation={citation}
            tenantId={tenantId}
            corpusId={corpusId}
          />
        );
      }

      lastIndex = match.index + match[0].length;
    }

    if (lastIndex < text.length) {
      parts.push(text.slice(lastIndex));
    }

    return parts.length > 0 ? parts : [text];
  };

  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        p: ({ children }) => (
          <p className="mb-2 last:mb-0">
            {Array.isArray(children)
              ? children.map((child, i) =>
                  typeof child === "string" ? (
                    <Fragment key={i}>{processText(child)}</Fragment>
                  ) : (
                    <Fragment key={i}>{child}</Fragment>
                  )
                )
              : typeof children === "string"
              ? processText(children)
              : children}
          </p>
        ),
        strong: ({ children }) => (
          <strong className="font-semibold">
            {Array.isArray(children)
              ? children.map((child, i) =>
                  typeof child === "string" ? (
                    <Fragment key={i}>{processText(child)}</Fragment>
                  ) : (
                    <Fragment key={i}>{child}</Fragment>
                  )
                )
              : typeof children === "string"
              ? processText(children)
              : children}
          </strong>
        ),
        li: ({ children }) => (
          <li className="ml-4">
            {Array.isArray(children)
              ? children.map((child, i) =>
                  typeof child === "string" ? (
                    <Fragment key={i}>{processText(child)}</Fragment>
                  ) : (
                    <Fragment key={i}>{child}</Fragment>
                  )
                )
              : typeof children === "string"
              ? processText(children)
              : children}
          </li>
        ),
        ul: ({ children }) => <ul className="list-disc mb-2 space-y-1">{children}</ul>,
        ol: ({ children }) => <ol className="list-decimal mb-2 space-y-1">{children}</ol>,
        code: ({ className, children }) => {
          const isInline = !className;
          return isInline ? (
            <code className="bg-muted px-1 py-0.5 rounded text-sm font-mono">{children}</code>
          ) : (
            <code className="block bg-muted p-3 rounded text-sm font-mono whitespace-pre-wrap break-words mb-2">
              {children}
            </code>
          );
        },
        pre: ({ children }) => (
          <pre className="mb-3 p-3 bg-slate-100 dark:bg-slate-800 rounded-lg max-w-full border border-slate-200 dark:border-slate-700 overflow-hidden">
            <div className="whitespace-pre-wrap break-words overflow-x-auto">
              {children}
            </div>
          </pre>
        ),
      }}
    >
      {content}
    </ReactMarkdown>
  );
};

const ContextEngineeringPanel = ({
  onSearch,
  tenantId = "VeloDB Sample",
  corpusId = "velodb_docs",
}: ContextEngineeringPanelProps) => {
  const [query, setQuery] = useState("");
  const [isRunning, setIsRunning] = useState(false);
  const [currentStep, setCurrentStep] = useState<number>(-1);
  const [steps, setSteps] = useState<Record<ContextStepType, StepResult>>({
    system: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
    keyword: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
    semantic: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
    graph: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
  });
  const [response, setResponse] = useState<{
    text: string;
    citations: CitationItem[];
    processingTimeMs: number;
  } | null>(null);
  const [streamingContent, setStreamingContent] = useState("");
  const [totalTokens, setTotalTokens] = useState(0);
  const [showRawPrompt, setShowRawPrompt] = useState(false);
  const abortControllerRef = useRef<AbortController | null>(null);

  const stepOrder: ContextStepType[] = ["system", "keyword", "semantic", "graph"];

  const resetState = () => {
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
    }
    setCurrentStep(-1);
    setSteps({
      system: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
      keyword: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
      semantic: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
      graph: { status: "pending", tokens: 0, timeMs: 0, summary: "", details: [] },
    });
    setResponse(null);
    setStreamingContent("");
    setTotalTokens(0);
    setShowRawPrompt(false);
  };

  // Pre-fetch graph entities when query changes (debounced)
  // This warms the LLM entity extraction cache before user clicks Execute
  useEffect(() => {
    if (!query || query.length < 5) return;

    const timeoutId = setTimeout(async () => {
      try {
        // Fire and forget - don't await, just warm the cache
        fetch(`${API_BASE_URL}/api/v1/query/prefetch`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            query,
            tenant_id: tenantId,
            corpus_id: corpusId,
          }),
        }).catch(() => {}); // Ignore errors - this is just cache warming
      } catch {
        // Ignore prefetch errors
      }
    }, 500); // Debounce 500ms

    return () => clearTimeout(timeoutId);
  }, [query, tenantId, corpusId]);

  // Extract all unique entities from graph chunks for visualization
  const allEntities = useMemo(() => {
    const entitySet = new Set<string>();
    steps.graph.chunks?.forEach((chunk) => {
      chunk.entity_names?.forEach((e) => {
        if (e.length >= 3 && e.length <= 35 && !ENTITY_NOISE_PATTERN.test(e)) {
          entitySet.add(e);
        }
      });
    });
    return Array.from(entitySet).slice(0, 12);
  }, [steps.graph.chunks]);

  // Generate the assembled raw prompt with all context
  const generateRawPrompt = () => {
    const sections: { label: string; color: string; bgColor: string; content: string }[] = [];

    // System prompt
    sections.push({
      label: "System Prompt",
      color: "text-blue-700",
      bgColor: "bg-blue-50 border-blue-200",
      content: SYSTEM_PROMPT,
    });

    // User query
    sections.push({
      label: "User Query",
      color: "text-slate-700",
      bgColor: "bg-slate-50 border-slate-200",
      content: query,
    });

    // Keyword results with highlighting
    if (steps.keyword.chunks && steps.keyword.chunks.length > 0) {
      // Extract keywords from query (filter stop words)
      const stopWords = new Set(["what", "is", "the", "a", "an", "in", "on", "at", "to", "for", "of", "and", "or", "how", "does", "do", "can", "will"]);
      const queryKeywords = query
        .toLowerCase()
        .split(/\s+/)
        .filter(w => w.length > 2 && !stopWords.has(w));

      // Function to highlight keywords in text
      const highlightKeywords = (text: string): string => {
        let result = text;
        queryKeywords.forEach(keyword => {
          // Case-insensitive replace with 【highlighted】 markers
          const regex = new RegExp(`(${keyword})`, "gi");
          result = result.replace(regex, "【$1】");
        });
        return result;
      };

      const keywordContent = `=== Matched Keywords ===\n${queryKeywords.join(", ") || "(none)"}\n\n=== Chunks ===\n` +
        steps.keyword.chunks
          .map((c, i) => {
            const highlighted = highlightKeywords(c.content.slice(0, 250));
            return `[K${i + 1}] ${highlighted}${c.content.length > 250 ? "..." : ""}`;
          })
          .join("\n\n");
      sections.push({
        label: `Keyword Search (${steps.keyword.chunks.length} chunks)`,
        color: "text-amber-700",
        bgColor: "bg-amber-50 border-amber-200",
        content: keywordContent,
      });
    }

    // Semantic results
    if (steps.semantic.chunks && steps.semantic.chunks.length > 0) {
      const semanticContent = steps.semantic.chunks
        .map((c, i) => `[S${i + 1}] ${c.content.slice(0, 200)}${c.content.length > 200 ? "..." : ""}`)
        .join("\n\n");
      sections.push({
        label: `Semantic Search (${steps.semantic.chunks.length} chunks)`,
        color: "text-purple-700",
        bgColor: "bg-purple-50 border-purple-200",
        content: semanticContent,
      });
    }

    // Graph results are shown in the dedicated Knowledge Graph Traversal panel above
    // (removed from generateRawPrompt to avoid duplication)

    return sections;
  };

  // Step 1: System Prompt (fixed content)
  const fetchSystemPrompt = async (): Promise<StepResult> => {
    const systemPromptTokens = 245; // Approximate from backend SYSTEM_PROMPT
    return {
      status: "complete",
      tokens: systemPromptTokens,
      timeMs: 0,
      summary: "Loaded VeloDB RAG assistant instructions",
      details: [
        "Role: VeloDB documentation expert",
        "Style: Technical, cite all sources",
        "Format: Structured with examples",
      ],
    };
  };

  // Step 2: Keyword Search (BM25)
  const fetchKeywordResults = async (searchQuery: string): Promise<StepResult> => {
    const startTime = performance.now();
    try {
      const response = await fetch(`${API_BASE_URL}/api/v1/search`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          query: searchQuery,
          method: "bm25",
          top_k: 5,
          tenant_id: tenantId,
          corpus_id: corpusId,
        }),
      });

      if (!response.ok) throw new Error(`API error: ${response.status}`);

      const data = await response.json();
      const chunks: ChunkResult[] = data.chunks || [];
      const timeMs = Math.round(performance.now() - startTime);

      // Calculate tokens from retrieved content
      const totalChunkTokens = chunks.reduce(
        (sum, chunk) => sum + estimateTokens(chunk.content || ""),
        0
      );

      return {
        status: "complete",
        tokens: totalChunkTokens,
        timeMs,
        summary: `Found ${chunks.length} chunks via BM25 inverted index`,
        details: chunks.slice(0, 3).map(
          (c) => `${c.doc_id} (score: ${c.score?.toFixed(2) || "N/A"})`
        ),
        chunks,
      };
    } catch (error) {
      return {
        status: "error",
        tokens: 0,
        timeMs: Math.round(performance.now() - startTime),
        summary: "Keyword search failed",
        details: [],
        error: error instanceof Error ? error.message : "Unknown error",
      };
    }
  };

  // Step 3: Semantic Search (Vector)
  const fetchSemanticResults = async (searchQuery: string): Promise<StepResult> => {
    const startTime = performance.now();
    try {
      const response = await fetch(`${API_BASE_URL}/api/v1/search`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          query: searchQuery,
          method: "vector",
          top_k: 5,
          tenant_id: tenantId,
          corpus_id: corpusId,
        }),
      });

      if (!response.ok) throw new Error(`API error: ${response.status}`);

      const data = await response.json();
      const chunks: ChunkResult[] = data.chunks || [];
      const timeMs = Math.round(performance.now() - startTime);

      const totalChunkTokens = chunks.reduce(
        (sum, chunk) => sum + estimateTokens(chunk.content || ""),
        0
      );

      return {
        status: "complete",
        tokens: totalChunkTokens,
        timeMs,
        summary: `Found ${chunks.length} chunks via ANN vector search`,
        details: chunks.slice(0, 3).map(
          (c) => `${c.doc_id} (dist: ${c.score?.toFixed(3) || "N/A"})`
        ),
        chunks,
      };
    } catch (error) {
      return {
        status: "error",
        tokens: 0,
        timeMs: Math.round(performance.now() - startTime),
        summary: "Semantic search failed",
        details: [],
        error: error instanceof Error ? error.message : "Unknown error",
      };
    }
  };

  // Step 4: Knowledge Graph - K-hop BFS traversal via SubgraphRetriever
  // Shows: Query → Seed Entities → K-hop Edges → Retrieved Chunks
  const fetchGraphResults = async (searchQuery: string): Promise<StepResult> => {
    const startTime = performance.now();

    try {
      // Call the subgraph API for real K-hop BFS traversal
      const subgraphResponse = await fetch(`${API_BASE_URL}/api/v1/query/subgraph`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          query: searchQuery,
          tenant_id: tenantId,
          corpus_id: corpusId,
          max_hops: 2,
          max_chunks: 15,
        }),
      });

      if (!subgraphResponse.ok) throw new Error(`Subgraph API error: ${subgraphResponse.status}`);

      const subgraphData: SubgraphResult = await subgraphResponse.json();
      const timeMs = Math.round(performance.now() - startTime);

      // Get seed entities from response
      const seedEntities = (subgraphData.seed_entities || []).slice(0, 10);

      // Deduplicate edges by (source, relation, target) - ignore chunk provenance for display
      const edgeSet = new Set<string>();
      const edges: GraphEdge[] = [];
      (subgraphData.edges || []).forEach(e => {
        const edgeKey = `${e.source}|${e.relation}|${e.target}`;
        if (!edgeSet.has(edgeKey)) {
          edgeSet.add(edgeKey);
          edges.push({
            source: e.source,
            target: e.target,
            relation: e.relation,
          });
        }
      });

      // Convert chunks to our format
      const chunks: ChunkResult[] = (subgraphData.chunks || []).map(c => ({
        chunk_id: c.chunk_id,
        content: c.content,
        content_type: c.content_type,
        doc_id: c.doc_id,
        score: c.score || 0,
        entity_names: c.entity_names,
      }));

      // Calculate tokens
      const totalChunkTokens = chunks.reduce(
        (sum, chunk) => sum + estimateTokens(chunk.content || ""),
        0
      );

      // Format details showing the traversal path
      const details: string[] = [];
      if (seedEntities.length > 0) {
        details.push(`Seeds: ${seedEntities.slice(0, 4).join(", ")}`);
      }
      // Show K-hop edges
      if (edges.length > 0) {
        const edgeSample = edges.slice(0, 2).map(e => {
          const rel = e.relation.replace(/_/g, " ").toLowerCase();
          return `${e.source} →[${rel}]→ ${e.target}`;
        });
        details.push(...edgeSample);
      } else if (seedEntities.length > 0) {
        // No edges but have seeds - show entity→chunk connections
        details.push(`Direct entity→chunk lookup (no edges in graph)`);
      }

      return {
        status: "complete",
        tokens: totalChunkTokens,
        timeMs,
        summary: `${seedEntities.length} seeds → ${edges.length} edges → ${chunks.length} chunks`,
        details: details.length > 0 ? details : ["No graph data found"],
        chunks,
        graphEdges: edges,
        seedEntities,
      };
    } catch (error) {
      return {
        status: "error",
        tokens: 0,
        timeMs: Math.round(performance.now() - startTime),
        summary: "Graph retrieval failed",
        details: [],
        error: error instanceof Error ? error.message : "Unknown error",
      };
    }
  };

  // Step 5: Stream LLM Response
  const streamLLMResponse = async (searchQuery: string): Promise<void> => {
    abortControllerRef.current = new AbortController();

    const response = await fetch(`${API_BASE_URL}/api/v1/query/cited/stream`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query: searchQuery,
        tenant_id: tenantId,
        corpus_id: corpusId,
        top_k: 5,
        include_surrounding_context: false,
      }),
      signal: abortControllerRef.current.signal,
    });

    if (!response.ok) throw new Error(`API error: ${response.status}`);

    const reader = response.body?.getReader();
    if (!reader) throw new Error("No response body");

    const decoder = new TextDecoder();
    let buffer = "";
    let fullContent = "";
    let receivedCitations: CitationItem[] = [];
    let processingTimeMs = 0;

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";

      for (const line of lines) {
        if (line.startsWith("data: ")) {
          try {
            const data = JSON.parse(line.slice(6));
            if (data.type === "token") {
              fullContent += data.content;
              setStreamingContent(fullContent);
            } else if (data.type === "citations") {
              receivedCitations = data.citations;
            } else if (data.type === "done") {
              processingTimeMs = data.processing_time_ms || 0;
            }
          } catch {
            // Ignore parse errors
          }
        }
      }
    }

    setResponse({
      text: fullContent,
      citations: receivedCitations,
      processingTimeMs,
    });
    setStreamingContent("");
  };

  const runContextEngineering = useCallback(async () => {
    if (!query.trim()) return;

    resetState();
    setIsRunning(true);

    let runningTotal = 0;

    try {
      // Step 1: System Prompt (fast, runs first)
      setCurrentStep(0);
      setSteps((prev) => ({ ...prev, system: { ...prev.system, status: "loading" } }));
      const systemResult = await fetchSystemPrompt();
      runningTotal += systemResult.tokens;
      setTotalTokens(runningTotal);
      setSteps((prev) => ({ ...prev, system: systemResult }));

      // Steps 2-4: Run all 3 searches IN PARALLEL to reduce latency
      setCurrentStep(1);
      setSteps((prev) => ({
        ...prev,
        keyword: { ...prev.keyword, status: "loading" },
        semantic: { ...prev.semantic, status: "loading" },
        graph: { ...prev.graph, status: "loading" },
      }));

      // Execute all searches concurrently
      const [keywordResult, semanticResult, graphResult] = await Promise.all([
        fetchKeywordResults(query).then((result) => {
          setSteps((prev) => ({ ...prev, keyword: result }));
          return result;
        }),
        fetchSemanticResults(query).then((result) => {
          setSteps((prev) => ({ ...prev, semantic: result }));
          return result;
        }),
        fetchGraphResults(query).then((result) => {
          setSteps((prev) => ({ ...prev, graph: result }));
          return result;
        }),
      ]);

      // Update total tokens after all searches complete
      runningTotal += keywordResult.tokens + semanticResult.tokens + graphResult.tokens;
      setTotalTokens(runningTotal);
      setCurrentStep(3);

      // Step 5: LLM Response
      setCurrentStep(4);
      await streamLLMResponse(query);

    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") {
        // User cancelled
        return;
      }
      console.error("Context engineering error:", error);
    } finally {
      setIsRunning(false);
      setCurrentStep(-1);
    }
  }, [query, tenantId, corpusId]);

  const handleDemoQuery = (q: string) => {
    setQuery(q);
    resetState();
  };

  return (
    <Card className="flex flex-col">
      <CardHeader className="pb-3 flex-shrink-0">
        <CardTitle className="flex items-center gap-2 text-lg">
          <Database className="w-5 h-5 text-cyan-600" />
          Context Engineering
        </CardTitle>
        <p className="text-sm text-muted-foreground">
          Watch VeloDB build context step-by-step using real API calls
        </p>
      </CardHeader>

      <CardContent className="flex flex-col space-y-4 pb-4">
        {/* Query Input */}
        <div className="space-y-2 flex-shrink-0">
          <div className="flex gap-2">
            <Input
              placeholder="Enter a question..."
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && !isRunning && runContextEngineering()}
              className="flex-1"
              disabled={isRunning}
            />
            <Button
              onClick={runContextEngineering}
              disabled={isRunning || !query.trim()}
              className="bg-cyan-600 hover:bg-cyan-700"
            >
              {isRunning ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : (
                <>
                  <Play className="w-4 h-4 mr-1" />
                  Run
                </>
              )}
            </Button>
            {(response || totalTokens > 0) && (
              <Button
                variant="outline"
                onClick={resetState}
                disabled={isRunning}
              >
                <RotateCcw className="w-4 h-4" />
              </Button>
            )}
          </div>

          {/* Demo queries */}
          <div className="flex flex-wrap gap-2">
            <span className="text-xs text-muted-foreground">Try:</span>
            {DEMO_QUERIES.map((q) => (
              <button
                key={q}
                onClick={() => handleDemoQuery(q)}
                disabled={isRunning}
                className="text-xs px-2 py-1 rounded-full bg-gray-100 hover:bg-gray-200 text-muted-foreground transition-colors disabled:opacity-50"
              >
                {q.length > 30 ? q.slice(0, 30) + "..." : q}
              </button>
            ))}
          </div>
        </div>

        {/* Scrollable Content Area */}
        <div className="space-y-4">
            {/* Context Building Steps */}
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-medium text-muted-foreground">
                  Context Building
                </h3>
                {totalTokens > 0 && (
                  <span className="text-xs font-mono">
                    <span className={cn(
                      "font-semibold",
                      totalTokens > 6000 ? "text-red-600" :
                      totalTokens > 4000 ? "text-amber-600" : "text-green-600"
                    )}>
                      {totalTokens.toLocaleString()}
                    </span>
                    <span className="text-muted-foreground"> / 8,192 tokens</span>
                  </span>
                )}
              </div>

              {/* Token Progress Bar */}
              {totalTokens > 0 && (
                <div className="h-2 bg-gray-100 rounded-full overflow-hidden">
                  <div
                    className={cn(
                      "h-full transition-all duration-500",
                      totalTokens > 6000 ? "bg-red-400" :
                      totalTokens > 4000 ? "bg-amber-400" : "bg-cyan-400"
                    )}
                    style={{ width: `${Math.min((totalTokens / 8192) * 100, 100)}%` }}
                  />
                </div>
              )}

              {/* Step Cards */}
              <div className="space-y-2">
                {stepOrder.map((stepType) => (
                  <ContextStepCard
                    key={stepType}
                    step={stepType}
                    status={steps[stepType].status === "error" ? "complete" : steps[stepType].status}
                    tokens={steps[stepType].tokens}
                    summary={steps[stepType].error || steps[stepType].summary}
                    details={steps[stepType].details}
                    timeMs={steps[stepType].timeMs}
                    mermaid={steps[stepType].mermaid}
                    chunks={steps[stepType].chunks?.map(c => ({
                      chunk_id: c.chunk_id,
                      content: c.content,
                      doc_id: c.doc_id,
                      score: c.score,
                      entity_names: c.entity_names,
                      relationships: c.relationships,
                    })) || []}
                    systemPromptContent={stepType === "system" ? SYSTEM_PROMPT : undefined}
                    graphEdges={stepType === "graph" ? steps.graph.graphEdges : undefined}
                    seedEntities={stepType === "graph" ? steps.graph.seedEntities : undefined}
                    query={stepType === "graph" ? query : undefined}
                  />
                ))}
              </div>

              {/* Raw Prompt View Button */}
              {totalTokens > 0 && (
                <Collapsible open={showRawPrompt} onOpenChange={setShowRawPrompt}>
                  <CollapsibleTrigger asChild>
                    <button
                      className={cn(
                        "flex items-center gap-2 text-xs font-medium px-3 py-2 rounded-lg transition-colors w-full justify-between",
                        showRawPrompt
                          ? "bg-slate-800 text-white"
                          : "bg-gray-100 text-gray-700 hover:bg-gray-200"
                      )}
                    >
                      <span className="flex items-center gap-2">
                        <Code className="w-4 h-4" />
                        View Assembled Prompt (Raw Context)
                      </span>
                      {showRawPrompt ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
                    </button>
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <div className="mt-3 space-y-3 p-3 bg-slate-900 rounded-lg">
                      <div className="text-xs text-slate-400 font-medium">
                        This is the assembled context sent to the LLM:
                      </div>

                      {/* Knowledge Graph Traversal - Horizontal flow with daylight theme */}
                      {(steps.graph.graphEdges?.length || steps.graph.seedEntities?.length || steps.graph.chunks?.length) && (
                        <div className="rounded-lg overflow-hidden border border-cyan-300 bg-cyan-50">
                          <div className="px-3 py-2 bg-cyan-100 border-b border-cyan-200 flex items-center gap-2">
                            <GitBranch className="w-4 h-4 text-cyan-700" />
                            <span className="text-xs font-medium text-cyan-700">Knowledge Graph Traversal</span>
                            <span className="text-[10px] text-slate-500 ml-auto">
                              {steps.graph.seedEntities?.length || 0} seeds → {steps.graph.graphEdges?.length || 0} edges → {steps.graph.chunks?.length || 0} chunks
                            </span>
                          </div>
                          <div className="p-3 space-y-2 max-h-64 overflow-y-auto font-mono text-xs">
                            {/* Query at top */}
                            <div className="text-slate-600 mb-2">
                              Query: <span className="text-cyan-800 font-semibold">"{query}"</span>
                            </div>

                            {/* Horizontal flow paths: Seed → [relation] → Hop → Chunk briefs */}
                            {steps.graph.graphEdges && steps.graph.graphEdges.length > 0 ? (
                              <div className="space-y-2">
                                {steps.graph.graphEdges.slice(0, 6).map((edge, idx) => {
                                  // Build entity→chunk mapping
                                  const entityToChunks = new Map<string, number[]>();
                                  steps.graph.chunks?.forEach((chunk, chunkIdx) => {
                                    (chunk.entity_names || []).forEach(e => {
                                      const key = e.toLowerCase();
                                      if (!entityToChunks.has(key)) entityToChunks.set(key, []);
                                      entityToChunks.get(key)!.push(chunkIdx);
                                    });
                                  });

                                  // Determine seed vs hop
                                  const seedSet = new Set((steps.graph.seedEntities || []).map(s => s.toLowerCase()));
                                  const isSeedSource = seedSet.has(edge.source.toLowerCase());
                                  const seed = isSeedSource ? edge.source : edge.target;
                                  const hopEntity = isSeedSource ? edge.target : edge.source;

                                  // Get chunk briefs for hop entity
                                  const chunkIndices = entityToChunks.get(hopEntity.toLowerCase()) || [];
                                  const chunkBriefs = chunkIndices.slice(0, 2).map(ci => ({
                                    idx: ci + 1,
                                    brief: steps.graph.chunks?.[ci]?.content.slice(0, 35).replace(/\n/g, " ") || ""
                                  }));

                                  return (
                                    <div key={idx} className="space-y-0.5">
                                      {/* Main flow line */}
                                      <div className="flex items-center gap-1.5 flex-wrap">
                                        <span className="px-2 py-0.5 bg-emerald-100 text-emerald-700 rounded border border-emerald-300">
                                          {seed}
                                        </span>
                                        <span className="text-slate-400">──[</span>
                                        <span className="text-amber-700 font-medium">{edge.relation.replace(/_/g, " ").toLowerCase()}</span>
                                        <span className="text-slate-400">]──→</span>
                                        <span className="px-2 py-0.5 bg-teal-100 text-teal-700 rounded border border-teal-300">
                                          {hopEntity}
                                        </span>
                                        {chunkBriefs.length > 0 && <span className="text-slate-400">──→</span>}
                                      </div>
                                      {/* Chunk briefs */}
                                      {chunkBriefs.map(({ idx: ci, brief }) => (
                                        <div key={ci} className="ml-8 flex items-center gap-1.5">
                                          <span className="text-cyan-700 font-semibold">[{ci}]</span>
                                          <span className="truncate text-slate-500">"{brief}..."</span>
                                        </div>
                                      ))}
                                    </div>
                                  );
                                })}
                                {steps.graph.graphEdges.length > 6 && (
                                  <div className="text-slate-500 text-center">... +{steps.graph.graphEdges.length - 6} more paths</div>
                                )}
                              </div>
                            ) : (
                              /* Fallback: show entities if no edges */
                              allEntities.length > 0 && (
                                <div className="space-y-1">
                                  {allEntities.slice(0, 6).map((entity, idx) => (
                                    <div key={idx} className="flex items-center gap-2">
                                      <span className="px-2 py-0.5 bg-emerald-100 text-emerald-700 rounded border border-emerald-300">
                                        {entity}
                                      </span>
                                    </div>
                                  ))}
                                </div>
                              )
                            )}

                            {/* Summary */}
                            <div className="pt-2 border-t border-cyan-200 text-slate-500 text-[10px] text-center">
                              {steps.graph.seedEntities?.length || 0} seeds → {steps.graph.graphEdges?.length || 0} edges → {steps.graph.chunks?.length || 0} chunks
                            </div>
                          </div>
                        </div>
                      )}

                      {generateRawPrompt().map((section, idx) => (
                        <div
                          key={idx}
                          className={cn("p-3 rounded-lg border", section.bgColor)}
                        >
                          <div className={cn("text-xs font-bold mb-2 flex items-center gap-2", section.color)}>
                            <span className={cn(
                              "w-2 h-2 rounded-full",
                              section.color.replace("text-", "bg-").replace("-700", "-500")
                            )} />
                            {section.label}
                          </div>
                          <pre className={cn(
                            "text-xs font-mono whitespace-pre-wrap overflow-x-auto max-h-32 overflow-y-auto",
                            section.color.replace("-700", "-800")
                          )}>
                            {renderWithHighlights(section.content)}
                          </pre>
                        </div>
                      ))}
                      <div className="text-xs text-slate-500 text-center pt-2 border-t border-slate-700">
                        Total: {totalTokens.toLocaleString()} tokens assembled from {
                          [steps.keyword.chunks?.length || 0, steps.semantic.chunks?.length || 0, steps.graph.chunks?.length || 0]
                            .filter(n => n > 0).reduce((a, b) => a + b, 0)
                        } chunks
                      </div>
                    </div>
                  </CollapsibleContent>
                </Collapsible>
              )}
            </div>

            {/* Arrow to Response */}
            {(response || streamingContent) && (
              <div className="flex justify-center py-2">
                <ArrowDown className="w-5 h-5 text-cyan-500 animate-bounce" />
              </div>
            )}

            {/* Streaming Response */}
            {streamingContent && !response && (
              <Card className="border-cyan-200 bg-gradient-to-br from-cyan-50/50 to-white">
                <CardContent className="p-4">
                  <div className="flex items-center justify-between flex-wrap gap-2 mb-3">
                    <div className="flex items-center gap-2">
                      <Sparkles className="w-4 h-4 text-cyan-600 animate-pulse" />
                      <span className="font-medium text-cyan-700">Generating Response...</span>
                    </div>
                    <span className="text-xs text-slate-500 bg-slate-100 px-2 py-1 rounded-full">
                      amazon/nova-micro-v1
                    </span>
                  </div>
                  <div className="prose prose-sm max-w-none">
                    <p className="text-sm text-foreground whitespace-pre-line">
                      {streamingContent}
                      <span className="inline-block w-2 h-4 ml-1 bg-cyan-500 animate-pulse" />
                    </p>
                  </div>
                </CardContent>
              </Card>
            )}

            {/* Final LLM Response */}
            {response && (
              <Card className="border-cyan-200 bg-gradient-to-br from-cyan-50/50 to-white">
                <CardContent className="p-4 space-y-4">
                  {/* Header with Model Info */}
                  <div className="flex items-center justify-between flex-wrap gap-2">
                    <div className="flex items-center gap-2">
                      <Sparkles className="w-4 h-4 text-cyan-600" />
                      <span className="font-medium text-cyan-700">LLM Response</span>
                    </div>
                    <div className="flex items-center gap-2">
                      {/* Model Badge */}
                      <a
                        href="https://openrouter.ai/amazon/nova-micro-v1"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex items-center gap-1.5 px-2 py-1 bg-slate-100 hover:bg-slate-200 rounded-full text-xs transition-colors group"
                        title="View on OpenRouter"
                      >
                        <span className="font-medium text-slate-700">nova-micro-v1</span>
                        <span className="text-slate-300">|</span>
                        <span className="text-emerald-600 font-medium">$0.035/M in</span>
                        <span className="text-amber-600 font-medium">$0.14/M out</span>
                        <ExternalLink className="w-3 h-3 text-slate-400 group-hover:text-slate-600" />
                      </a>
                      {response.processingTimeMs > 0 && (
                        <span className="text-xs font-mono text-muted-foreground bg-slate-100 px-2 py-1 rounded-full">
                          {(response.processingTimeMs / 1000).toFixed(1)}s
                        </span>
                      )}
                    </div>
                  </div>

                  {/* Response Text with Citations */}
                  <div className="prose prose-sm max-w-none">
                    <MarkdownWithCitations
                      content={response.text}
                      citations={response.citations}
                      tenantId={tenantId}
                      corpusId={corpusId}
                    />
                  </div>

                  {/* Citations Legend */}
                  {response.citations.length > 0 && (
                    <div className="border-t pt-3">
                      <div className="text-xs font-medium text-muted-foreground mb-2">
                        Sources ({response.citations.length})
                      </div>
                      <div className="flex flex-wrap gap-2">
                        {response.citations.map((citation) => {
                          const config = citationConfig[citation.type] || citationConfig.text;
                          const Icon = config.icon;
                          return (
                            <span
                              key={citation.ref_id}
                              className={cn(
                                "flex items-center gap-1 text-xs px-2 py-1 rounded-full",
                                config.bgColor,
                                config.color
                              )}
                            >
                              <Icon className="w-3 h-3" />
                              [{citation.ref_id}] {citation.document_name || "Source"}
                            </span>
                          );
                        })}
                      </div>
                    </div>
                  )}
                </CardContent>
              </Card>
            )}

          {/* Empty State */}
          {!response && currentStep === -1 && totalTokens === 0 && (
            <div className="text-center py-8 text-muted-foreground">
              <MessageSquare className="w-8 h-8 mx-auto mb-2 opacity-30" />
              <p className="text-sm">Enter a query to see context engineering in action</p>
              <p className="text-xs mt-1">Click [?] on any step to learn how VeloDB handles it</p>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
};

export default ContextEngineeringPanel;
