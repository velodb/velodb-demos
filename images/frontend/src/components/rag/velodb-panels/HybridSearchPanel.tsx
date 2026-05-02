import { useState, useCallback, useMemo, useRef, useEffect } from "react";
import { Search, Zap, FileText, Sparkles, Loader2, Clock, HelpCircle, ArrowRight, ArrowDown, Database, Binary, Play, Hash, Brain, Merge, List, Target, Trophy, CheckCircle2, AlertCircle } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import SearchResultCard, { SearchResult, SearchType } from "@/components/rag/SearchResultCard";
import ResultJourneyModal from "@/components/rag/ResultJourneyModal";
import { cn, RAG_API_BASE_URL as API_BASE_URL } from "@/lib/utils";

// Demo queries that prove hybrid search superiority
const SAMPLE_QUERIES = [
  // Q1: Hybrid > Keyword - BM25 returns stream-load/TPC-DS docs, Hybrid finds Runtime Filter docs
  "How does runtime filter help with large table joins?",
  // Q2: Hybrid > Vector - Vector returns generic image at #1, Hybrid finds exact "RuntimeFilterState = READY" at #1
  "RuntimeFilterState = READY",
];

// Method configurations
const METHOD_CONFIG = {
  bm25: {
    title: "BM25",
    subtitle: "Keyword",
    icon: FileText,
    bgColor: "bg-amber-50",
    borderColor: "border-amber-400",
    textColor: "text-amber-700",
    iconColor: "text-amber-600",
    buttonBg: "bg-amber-600 hover:bg-amber-700",
  },
  vector: {
    title: "Vector",
    subtitle: "Semantic",
    icon: Sparkles,
    bgColor: "bg-purple-50",
    borderColor: "border-purple-400",
    textColor: "text-purple-700",
    iconColor: "text-purple-600",
    buttonBg: "bg-purple-600 hover:bg-purple-700",
  },
  hybrid: {
    title: "Hybrid",
    subtitle: "RRF",
    icon: Zap,
    bgColor: "bg-cyan-50",
    borderColor: "border-cyan-400",
    textColor: "text-cyan-700",
    iconColor: "text-cyan-600",
    buttonBg: "bg-cyan-600 hover:bg-cyan-700",
  },
};

// Visual Explanation Modal with diagrams
const MethodExplainModal = ({
  method,
  isOpen,
  onClose
}: {
  method: "bm25" | "vector" | "hybrid" | null;
  isOpen: boolean;
  onClose: () => void;
}) => {
  if (!method) return null;

  // BM25 Visual Explanation
  const BM25Diagram = () => (
    <div className="space-y-4">
      {/* Query to Keywords */}
      <div className="flex items-center gap-3">
        <div className="flex-1 p-3 bg-amber-100 rounded-lg border-2 border-amber-300">
          <p className="text-xs text-amber-600 font-medium mb-1">Query</p>
          <p className="font-mono text-sm">"configure HNSW index"</p>
        </div>
        <ArrowRight className="w-5 h-5 text-amber-500" />
        <div className="flex-1 p-3 bg-amber-50 rounded-lg border border-amber-200">
          <p className="text-xs text-amber-600 font-medium mb-1">Tokenize</p>
          <div className="flex gap-1 flex-wrap">
            {["configure", "HNSW", "index"].map(t => (
              <span key={t} className="px-2 py-0.5 bg-amber-200 rounded text-xs font-mono">{t}</span>
            ))}
          </div>
        </div>
      </div>

      {/* Inverted Index Lookup */}
      <div className="p-3 bg-slate-50 rounded-lg border">
        <p className="text-xs text-slate-500 font-medium mb-2 flex items-center gap-1">
          <Hash className="w-3 h-3" /> Inverted Index Lookup
        </p>
        <div className="grid grid-cols-3 gap-2 text-xs font-mono">
          <div className="p-2 bg-white rounded border">
            <span className="text-amber-600">configure</span>
            <span className="text-slate-400"> → </span>
            <span className="text-slate-600">[doc1, doc3, doc7]</span>
          </div>
          <div className="p-2 bg-white rounded border">
            <span className="text-amber-600">HNSW</span>
            <span className="text-slate-400"> → </span>
            <span className="text-slate-600">[doc1, doc2]</span>
          </div>
          <div className="p-2 bg-white rounded border">
            <span className="text-amber-600">index</span>
            <span className="text-slate-400"> → </span>
            <span className="text-slate-600">[doc1, doc2, doc5]</span>
          </div>
        </div>
      </div>

      {/* BM25 Scoring */}
      <div className="p-3 bg-amber-50 rounded-lg border border-amber-200">
        <p className="text-xs text-amber-600 font-medium mb-2">BM25 Scoring Formula</p>
        <div className="bg-slate-900 p-2 rounded text-xs font-mono text-amber-300">
          score = Σ IDF(term) × (TF × (k+1)) / (TF + k × (1-b+b×|D|/avgDL))
        </div>
        <p className="text-xs text-slate-500 mt-2">Ranks by term frequency & document length normalization</p>
      </div>

      {/* SQL */}
      <div className="p-3 bg-slate-900 rounded-lg">
        <p className="text-xs text-slate-400 mb-2">SQL Query</p>
        <pre className="text-xs font-mono text-amber-300">
{`SELECT chunk_id, content, score()
FROM rag_chunks
WHERE MATCH_ANY(content, ?)
ORDER BY score() DESC`}
        </pre>
      </div>
    </div>
  );

  // Vector Visual Explanation
  const VectorDiagram = () => (
    <div className="space-y-4">
      {/* Query to Embedding */}
      <div className="flex items-center gap-3">
        <div className="flex-1 p-3 bg-purple-100 rounded-lg border-2 border-purple-300">
          <p className="text-xs text-purple-600 font-medium mb-1">Query</p>
          <p className="font-mono text-sm">"configure HNSW index"</p>
        </div>
        <div className="flex flex-col items-center">
          <Brain className="w-5 h-5 text-purple-500" />
          <span className="text-[10px] text-purple-500">BGE-M3</span>
        </div>
        <div className="flex-1 p-3 bg-purple-50 rounded-lg border border-purple-200">
          <p className="text-xs text-purple-600 font-medium mb-1">Embedding (1024-dim)</p>
          <p className="font-mono text-[10px] text-slate-500">[0.023, -0.891, 0.445, ... ]</p>
        </div>
      </div>

      {/* Vector Space Visualization */}
      <div className="p-3 bg-slate-50 rounded-lg border">
        <p className="text-xs text-slate-500 font-medium mb-2 flex items-center gap-1">
          <Target className="w-3 h-3" /> Vector Space (ANN Search)
        </p>
        <div className="relative h-32 bg-white rounded border overflow-hidden">
          {/* Query point */}
          <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2">
            <div className="w-4 h-4 bg-purple-500 rounded-full animate-pulse" />
            <span className="absolute -top-4 left-1/2 -translate-x-1/2 text-[10px] text-purple-600 whitespace-nowrap">Query</span>
          </div>
          {/* Nearest neighbors */}
          {[
            { x: "35%", y: "40%", dist: "0.12", rank: 1 },
            { x: "60%", y: "55%", dist: "0.18", rank: 2 },
            { x: "45%", y: "70%", dist: "0.23", rank: 3 },
            { x: "70%", y: "35%", dist: "0.31", rank: 4 },
          ].map((p, i) => (
            <div key={i} className="absolute" style={{ left: p.x, top: p.y }}>
              <div className="w-3 h-3 bg-slate-300 rounded-full border-2 border-purple-300" />
              <span className="absolute -bottom-3 left-1/2 -translate-x-1/2 text-[9px] text-slate-500">#{p.rank}</span>
            </div>
          ))}
          {/* Distance lines */}
          <svg className="absolute inset-0 w-full h-full">
            <line x1="50%" y1="50%" x2="35%" y2="40%" stroke="#a855f7" strokeWidth="1" strokeDasharray="3" opacity="0.5" />
            <line x1="50%" y1="50%" x2="60%" y2="55%" stroke="#a855f7" strokeWidth="1" strokeDasharray="3" opacity="0.3" />
          </svg>
        </div>
        <p className="text-xs text-slate-500 mt-2 text-center">Find k-nearest neighbors by L2 distance</p>
      </div>

      {/* SQL */}
      <div className="p-3 bg-slate-900 rounded-lg">
        <p className="text-xs text-slate-400 mb-2">SQL Query</p>
        <pre className="text-xs font-mono text-purple-300">
{`SELECT chunk_id, content,
       l2_distance(embedding, ?) AS dist
FROM rag_chunks
ORDER BY dist ASC`}
        </pre>
      </div>
    </div>
  );

  // Hybrid Visual Explanation
  const HybridDiagram = () => (
    <div className="space-y-4">
      {/* Dual Path */}
      <div className="grid grid-cols-2 gap-3">
        <div className="p-3 bg-amber-50 rounded-lg border border-amber-200">
          <div className="flex items-center gap-2 mb-2">
            <FileText className="w-4 h-4 text-amber-600" />
            <span className="text-xs font-medium text-amber-700">BM25 Path</span>
          </div>
          <div className="space-y-1 text-xs">
            <div className="flex justify-between"><span>doc_2</span><span className="text-amber-600">rank #1</span></div>
            <div className="flex justify-between"><span>doc_5</span><span className="text-amber-600">rank #2</span></div>
            <div className="flex justify-between"><span>doc_1</span><span className="text-amber-600">rank #3</span></div>
          </div>
        </div>
        <div className="p-3 bg-purple-50 rounded-lg border border-purple-200">
          <div className="flex items-center gap-2 mb-2">
            <Sparkles className="w-4 h-4 text-purple-600" />
            <span className="text-xs font-medium text-purple-700">Vector Path</span>
          </div>
          <div className="space-y-1 text-xs">
            <div className="flex justify-between"><span>doc_1</span><span className="text-purple-600">rank #1</span></div>
            <div className="flex justify-between"><span>doc_2</span><span className="text-purple-600">rank #2</span></div>
            <div className="flex justify-between"><span>doc_3</span><span className="text-purple-600">rank #3</span></div>
          </div>
        </div>
      </div>

      {/* RRF Fusion */}
      <div className="flex justify-center">
        <ArrowDown className="w-5 h-5 text-cyan-500" />
      </div>

      <div className="p-3 bg-cyan-50 rounded-lg border-2 border-cyan-300">
        <div className="flex items-center gap-2 mb-2">
          <Merge className="w-4 h-4 text-cyan-600" />
          <span className="text-xs font-medium text-cyan-700">RRF Fusion (k=60)</span>
        </div>
        <div className="bg-slate-900 p-2 rounded text-xs font-mono text-cyan-300 mb-2">
          RRF(d) = 1/(60+rank_bm25) + 1/(60+rank_vector)
        </div>
        <div className="space-y-1 text-xs">
          <div className="flex justify-between items-center">
            <span className="font-medium">doc_2</span>
            <span className="text-slate-500">1/(60+1) + 1/(60+2) = 0.0164 + 0.0161 =</span>
            <span className="text-cyan-600 font-bold">0.0325</span>
          </div>
          <div className="flex justify-between items-center">
            <span className="font-medium">doc_1</span>
            <span className="text-slate-500">1/(60+3) + 1/(60+1) = 0.0159 + 0.0164 =</span>
            <span className="text-cyan-600 font-bold">0.0323</span>
          </div>
        </div>
      </div>

      {/* Result */}
      <div className="p-3 bg-slate-50 rounded-lg border">
        <div className="flex items-center gap-2 mb-2">
          <List className="w-4 h-4 text-slate-600" />
          <span className="text-xs font-medium text-slate-700">Final Ranking</span>
        </div>
        <div className="flex gap-2">
          {["doc_2", "doc_1", "doc_5", "doc_3"].map((d, i) => (
            <div key={d} className={cn(
              "px-2 py-1 rounded text-xs font-mono",
              i === 0 ? "bg-cyan-100 text-cyan-700 font-bold" : "bg-slate-100"
            )}>
              #{i+1} {d}
            </div>
          ))}
        </div>
      </div>

      {/* SQL */}
      <div className="p-3 bg-slate-900 rounded-lg">
        <p className="text-xs text-slate-400 mb-2">SQL Query (One Query for All!)</p>
        <pre className="text-[10px] font-mono text-cyan-300 overflow-x-auto whitespace-pre-wrap">
{`SELECT candidate_name, resume_text,
       bm25_score(resume_text) as keyword_score,
       cosine_distance(embedding, query_vec) as vector_score
FROM resumes
WHERE
    -- Step 1: Structured filter (B-tree index, ~50ms)
    location = 'San Francisco' AND seniority = 'Senior'
    -- 100M → 2M candidates

    -- Step 2: Keyword filter (inverted index, ~200ms)
    AND resume_text MATCH_ANY 'Python'
    -- 2M → 15K candidates

    -- Step 3: Vector similarity (on filtered set, ~100ms)
    AND cosine_distance(embedding, query_vec) < 0.3
    -- 15K → 100 candidates

ORDER BY (keyword_score * 0.3 + vector_score * 0.7) DESC
LIMIT 10;`}
        </pre>
        <p className="text-xs text-slate-500 mt-2">Combines structured, keyword & vector search in ONE query</p>
      </div>
    </div>
  );

  const config = METHOD_CONFIG[method];
  const Icon = config.icon;

  return (
    <Dialog open={isOpen} onOpenChange={onClose}>
      <DialogContent className="max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <div className={cn("p-1.5 rounded", config.bgColor)}>
              <Icon className={cn("w-4 h-4", config.iconColor)} />
            </div>
            <span className={config.textColor}>{config.title}</span>
            <span className="text-muted-foreground font-normal text-sm">— How it works</span>
          </DialogTitle>
        </DialogHeader>

        <div className="mt-2">
          {method === "bm25" && <BM25Diagram />}
          {method === "vector" && <VectorDiagram />}
          {method === "hybrid" && <HybridDiagram />}
        </div>
      </DialogContent>
    </Dialog>
  );
};

// API Configuration
const DEFAULT_TENANT = "VeloDB Sample";
const DEFAULT_CORPUS = "velodb_docs";

// Response types for separate search results
interface SearchResultItem {
  chunk_id: string;
  doc_id: string;
  content: string;
  content_type: string;  // text, image, table, code, formula
  score: number;
  page_number?: number;
}

interface SeparateSearchResponse {
  query: string;
  vector_results: SearchResultItem[];
  bm25_results: SearchResultItem[];
  hybrid_results: SearchResultItem[];
  total_vector_results: number;
  total_bm25_results: number;
  total_hybrid_results: number;
  processing_time_ms: number;
  // Individual timing per method
  vector_time_ms?: number;
  bm25_time_ms?: number;
  hybrid_time_ms?: number;
}

// Fetch all three search methods at once using return_separate_results
const searchAllMethods = async (
  query: string
): Promise<{
  vector: { results: SearchResult[]; timeMs: number };
  bm25: { results: SearchResult[]; timeMs: number };
  hybrid: { results: SearchResult[]; timeMs: number };
}> => {
  const startTime = performance.now();
  try {
    const response = await fetch(`${API_BASE_URL}/api/v1/search`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query,
        top_k: 5,
        method: "hybrid",
        return_separate_results: true,
        tenant_id: DEFAULT_TENANT,
        corpus_id: DEFAULT_CORPUS,
      }),
    });
    if (!response.ok) throw new Error("Search failed");
    const data: SeparateSearchResponse = await response.json();
    const fallbackTime = Math.round(performance.now() - startTime);

    // Use individual timing from API, fallback to total time
    const vectorTime = Math.round(data.vector_time_ms || fallbackTime / 3);
    const bm25Time = Math.round(data.bm25_time_ms || fallbackTime / 3);
    const hybridTime = Math.round(data.hybrid_time_ms || fallbackTime / 3);

    const mapResults = (
      results: SearchResultItem[],
      method: SearchType
    ): SearchResult[] =>
      results.map((r, idx) => ({
        id: String(r.chunk_id || idx),
        rank: idx + 1,
        score: r.score || 0,
        text: r.content || "",
        documentName: r.doc_id || "unknown",
        chunkIndex: Number(r.chunk_id) || undefined,
        matchType: method,
        contentType: (r.content_type as any) || "text",  // Pass content type for multimodal display
      }));

    return {
      vector: { results: mapResults(data.vector_results || [], "vector"), timeMs: vectorTime },
      bm25: { results: mapResults(data.bm25_results || [], "bm25"), timeMs: bm25Time },
      hybrid: { results: mapResults(data.hybrid_results || [], "hybrid"), timeMs: hybridTime },
    };
  } catch (e) {
    console.warn("API failed, using mock data:", e);
    // Fall back to mock for all methods
    const [vector, bm25, hybrid] = await Promise.all([
      mockSearch(query, "vector"),
      mockSearch(query, "bm25"),
      mockSearch(query, "hybrid"),
    ]);
    return { vector, bm25, hybrid };
  }
};

// Single method search (fallback)
const searchAPI = async (query: string, method: SearchType): Promise<{ results: SearchResult[]; timeMs: number }> => {
  const startTime = performance.now();
  try {
    const response = await fetch(`${API_BASE_URL}/api/v1/search`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query,
        top_k: 5,
        method: method,
        tenant_id: DEFAULT_TENANT,
        corpus_id: DEFAULT_CORPUS,
      }),
    });
    if (!response.ok) throw new Error("Search failed");
    const data = await response.json();
    // API returns 'chunks' not 'results'
    const results: SearchResult[] = (data.chunks || []).map((r: any, idx: number) => ({
      id: String(r.chunk_id || idx), rank: idx + 1, score: r.score || 0, text: r.content || "",
      documentName: r.doc_id || "unknown", chunkIndex: r.chunk_id, matchType: method,
      contentType: r.content_type || "text",  // Pass content type for multimodal display
    }));
    return { results, timeMs: Math.round(performance.now() - startTime) };
  } catch {
    return mockSearch(query, method);
  }
};

const mockSearch = async (query: string, mode: SearchType): Promise<{ results: SearchResult[]; timeMs: number }> => {
  await new Promise((r) => setTimeout(r, 300 + Math.random() * 200));
  // Mock data with VeloDB-related content showing different rankings
  const base = [
    { id: "1", text: "Hash bucketing distributes data across buckets using a hash function. This helps avoid data skew and improves parallel query execution.", doc: "bucketing.md", chunk: 12 },
    { id: "2", text: "Dynamic partitioning automatically creates partitions based on data characteristics, reducing manual partition management overhead.", doc: "partitioning.md", chunk: 5 },
    { id: "3", text: "Query performance optimization includes techniques like predicate pushdown, column pruning, and aggregate pushdown for faster analytics.", doc: "optimization.md", chunk: 8 },
    { id: "4", text: "Time series data benefits from range partitioning by timestamp. This enables efficient partition pruning for time-based queries.", doc: "timeseries.md", chunk: 3 },
    { id: "5", text: "Aggregate functions like SUM, AVG, and COUNT are pushed down to storage layer for distributed computation.", doc: "aggregates.md", chunk: 15 },
  ];
  // Different scoring to show how methods differ
  const scores: Record<string, Record<SearchType, number>> = {
    "1": { vector: 0.72, bm25: 0.95, hybrid: 0.85 }, // BM25 wins: exact keyword match
    "2": { vector: 0.88, bm25: 0.65, hybrid: 0.78 }, // Vector wins: semantic
    "3": { vector: 0.91, bm25: 0.70, hybrid: 0.82 }, // Vector wins: "speed up" = "optimization"
    "4": { vector: 0.85, bm25: 0.82, hybrid: 0.84 }, // Close match
    "5": { vector: 0.68, bm25: 0.88, hybrid: 0.79 }, // BM25 wins: SQL keywords
  };
  const results = base
    .map((r) => ({ ...r, score: scores[r.id][mode] }))
    .sort((a, b) => b.score - a.score)
    .map((r, idx) => ({ id: r.id, rank: idx + 1, score: r.score, text: r.text, documentName: r.doc, chunkIndex: r.chunk, matchType: mode }));
  return { results, timeMs: Math.round(300 + Math.random() * 200) };
};

// Comparison Stats Component - Shows evidence that hybrid is better
const ComparisonStats = ({
  bm25Results,
  vectorResults,
  hybridResults,
}: {
  bm25Results: SearchResult[];
  vectorResults: SearchResult[];
  hybridResults: SearchResult[];
}) => {
  // Calculate comparison metrics
  const bm25Ids = new Set(bm25Results.slice(0, 5).map(r => r.id));
  const vectorIds = new Set(vectorResults.slice(0, 5).map(r => r.id));
  const hybridIds = new Set(hybridResults.slice(0, 5).map(r => r.id));

  // What hybrid found that others missed
  const hybridOnlyFromBM25 = [...hybridIds].filter(id => !bm25Ids.has(id)).length;
  const hybridOnlyFromVector = [...hybridIds].filter(id => !vectorIds.has(id)).length;

  // Coverage: what % of hybrid results came from each method
  const fromBM25 = [...hybridIds].filter(id => bm25Ids.has(id)).length;
  const fromVector = [...hybridIds].filter(id => vectorIds.has(id)).length;
  const fromBoth = [...hybridIds].filter(id => bm25Ids.has(id) && vectorIds.has(id)).length;

  // Calculate best ranking: for each hybrid result, which method ranked it higher?
  const rankings = hybridResults.slice(0, 5).map(hr => {
    const bm25Rank = bm25Results.findIndex(r => r.id === hr.id) + 1 || 999;
    const vectorRank = vectorResults.findIndex(r => r.id === hr.id) + 1 || 999;
    return {
      id: hr.id,
      hybridRank: hr.rank,
      bm25Rank,
      vectorRank,
      bestSource: bm25Rank < vectorRank ? 'bm25' : bm25Rank > vectorRank ? 'vector' : 'both'
    };
  });

  // Count how many results would be missed by each individual method
  const missedByBM25Only = rankings.filter(r => r.bm25Rank > 5 && r.vectorRank <= 5).length;
  const missedByVectorOnly = rankings.filter(r => r.vectorRank > 5 && r.bm25Rank <= 5).length;

  // Simple insight: which method contributed more to hybrid results
  const bm25Dominant = fromBM25 > fromVector;
  const vectorDominant = fromVector > fromBM25;

  return (
    <div className="bg-gradient-to-r from-cyan-50 to-purple-50 dark:from-cyan-950/30 dark:to-purple-950/30 rounded-lg p-3 border border-cyan-200 dark:border-cyan-800">
      <div className="flex items-center gap-2">
        <Trophy className="w-4 h-4 text-cyan-600" />
        <span className="text-sm font-medium text-cyan-700 dark:text-cyan-300">Hybrid Search Advantage</span>
      </div>

      <div className="mt-2 flex items-start gap-2 text-xs">
        <CheckCircle2 className="w-3.5 h-3.5 text-green-600 mt-0.5 flex-shrink-0" />
        <span className="text-green-700 dark:text-green-400">
          {bm25Dominant ? (
            <><strong>Keyword match drove results</strong> - BM25 found {fromBM25} of {hybridIds.size} top results</>
          ) : vectorDominant ? (
            <><strong>Semantic understanding drove results</strong> - Vector found {fromVector} of {hybridIds.size} top results</>
          ) : (
            <><strong>Balanced fusion</strong> - Both methods contributed equally to top {hybridIds.size} results</>
          )}
        </span>
      </div>
    </div>
  );
};

interface HybridSearchPanelProps {
  onSearch?: (query: string, method: SearchType) => void;
}

const HybridSearchPanel = ({ onSearch }: HybridSearchPanelProps) => {
  const [query, setQuery] = useState("");
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [loading, setLoading] = useState<Record<SearchType, boolean>>({ bm25: false, vector: false, hybrid: false });
  const [results, setResults] = useState<Record<SearchType, SearchResult[]>>({ bm25: [], vector: [], hybrid: [] });
  const [times, setTimes] = useState<Record<SearchType, number | null>>({ bm25: null, vector: null, hybrid: null });
  const [hoveredChunkId, setHoveredChunkId] = useState<string | null>(null);
  const [selectedResult, setSelectedResult] = useState<SearchResult | null>(null);
  const [isJourneyModalOpen, setIsJourneyModalOpen] = useState(false);
  const [explainMethod, setExplainMethod] = useState<"bm25" | "vector" | "hybrid" | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const filteredSuggestions = useMemo(() => {
    if (!query.trim()) return SAMPLE_QUERIES;
    return SAMPLE_QUERIES.filter(s => s.toLowerCase().includes(query.toLowerCase()));
  }, [query]);

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (inputRef.current && !inputRef.current.contains(e.target as Node)) setShowSuggestions(false);
    };
    document.addEventListener("click", handleClick);
    return () => document.removeEventListener("click", handleClick);
  }, []);

  // Run all three search methods at once (recommended)
  const runAllSearches = useCallback(async () => {
    if (!query.trim()) return;
    setShowSuggestions(false);
    setLoading({ bm25: true, vector: true, hybrid: true });
    onSearch?.(query, "hybrid");
    try {
      const allResults = await searchAllMethods(query);
      setResults({
        vector: allResults.vector.results,
        bm25: allResults.bm25.results,
        hybrid: allResults.hybrid.results,
      });
      setTimes({
        vector: allResults.vector.timeMs,
        bm25: allResults.bm25.timeMs,
        hybrid: allResults.hybrid.timeMs,
      });
    } finally {
      setLoading({ bm25: false, vector: false, hybrid: false });
    }
  }, [query, onSearch]);

  // Run a single search method (fallback)
  const runSearch = useCallback(async (method: SearchType) => {
    if (!query.trim()) return;
    setShowSuggestions(false);
    setLoading(l => ({ ...l, [method]: true }));
    onSearch?.(query, method);
    try {
      const { results: r, timeMs } = await searchAPI(query, method);
      setResults(res => ({ ...res, [method]: r }));
      setTimes(t => ({ ...t, [method]: timeMs }));
    } finally {
      setLoading(l => ({ ...l, [method]: false }));
    }
  }, [query, onSearch]);

  // Compact button
  const SearchButton = ({ method }: { method: "bm25" | "vector" | "hybrid" }) => {
    const cfg = METHOD_CONFIG[method];
    const Icon = cfg.icon;
    const isLoading = loading[method];
    const time = times[method];
    const hasResults = results[method].length > 0;

    return (
      <div className={cn(
        "flex items-center gap-2 px-3 py-2 rounded-lg border transition-all",
        hasResults ? cn(cfg.borderColor, cfg.bgColor) : "border-border"
      )}>
        <Icon className={cn("w-4 h-4 flex-shrink-0", cfg.iconColor)} />
        <span className={cn("font-medium text-sm", cfg.textColor)}>{cfg.title}</span>
        <button
          onClick={(e) => { e.stopPropagation(); setExplainMethod(method); }}
          className="p-0.5 rounded hover:bg-black/5"
          title={`How ${cfg.title} works`}
        >
          <HelpCircle className="w-3.5 h-3.5 text-muted-foreground hover:text-foreground" />
        </button>
        <div className="flex-1" />
        {time !== null && (
          <span className="text-xs text-green-600 font-mono">{time}ms</span>
        )}
        <Button
          size="sm"
          onClick={() => runSearch(method)}
          disabled={isLoading || !query.trim()}
          className={cn("h-7 px-3 text-xs", cfg.buttonBg)}
        >
          {isLoading ? <Loader2 className="w-3 h-3 animate-spin" /> : <Play className="w-3 h-3" />}
        </Button>
      </div>
    );
  };

  // Handle result click - memoized to prevent re-renders
  const handleResultClick = useCallback((r: SearchResult) => {
    setSelectedResult(r);
    setIsJourneyModalOpen(true);
  }, []);

  // Hover handlers - memoized
  const handleMouseEnter = useCallback((id: string) => {
    setHoveredChunkId(id);
  }, []);

  const handleMouseLeave = useCallback(() => {
    setHoveredChunkId(null);
  }, []);

  // Results column - with hover to cross-highlight same chunk across columns
  const ResultsColumn = ({ method }: { method: "bm25" | "vector" | "hybrid" }) => {
    const cfg = METHOD_CONFIG[method];
    const Icon = cfg.icon;
    const r = results[method];
    const isLoading = loading[method];

    return (
      <div className="space-y-2">
        {isLoading ? (
          <div className="flex justify-center py-8">
            <Loader2 className={cn("w-5 h-5 animate-spin", cfg.iconColor)} />
          </div>
        ) : r.length > 0 ? (
          r.map((result, idx) => (
            <div
              key={`${method}-${result.id}`}
              onMouseEnter={() => handleMouseEnter(result.id)}
              onMouseLeave={handleMouseLeave}
            >
              <SearchResultCard
                result={result}
                index={idx}
                animationDelay={60}
                isHighlighted={hoveredChunkId === result.id}
                onClick={handleResultClick}
              />
            </div>
          ))
        ) : (
          <div className="flex flex-col items-center py-8 text-muted-foreground">
            <Icon className="w-6 h-6 mb-1 opacity-20" />
            <span className="text-xs">No results</span>
          </div>
        )}
      </div>
    );
  };

  return (
    <Card className="h-full">
      <CardHeader className="pb-2">
        <CardTitle className="text-base flex items-center gap-2">
          <Search className="w-4 h-4 text-cyan-600" />
          Search Comparison
        </CardTitle>
      </CardHeader>

      <CardContent className="space-y-3">
        {/* Search Input with Compare All button */}
        <div className="flex gap-2">
          <div className="relative flex-1" ref={inputRef}>
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
            <Input
              placeholder="Type a search query..."
              value={query}
              onChange={(e) => { setQuery(e.target.value); setShowSuggestions(true); }}
              onFocus={() => setShowSuggestions(true)}
              onKeyDown={(e) => {
                if (e.key === "Escape") setShowSuggestions(false);
                if (e.key === "Enter" && query.trim()) runAllSearches();
              }}
              className="pl-9 h-10"
            />
            {showSuggestions && filteredSuggestions.length > 0 && (
              <div className="absolute z-50 w-full mt-1 bg-white border rounded-lg shadow-lg">
                {filteredSuggestions.map((s, i) => (
                  <button
                    key={i}
                    onClick={() => { setQuery(s); setShowSuggestions(false); }}
                    className="w-full px-3 py-2 text-left text-sm hover:bg-muted flex items-center gap-2"
                  >
                    <Search className="w-3 h-3 text-muted-foreground" />
                    {s}
                  </button>
                ))}
              </div>
            )}
          </div>
          <Button
            onClick={runAllSearches}
            disabled={!query.trim() || Object.values(loading).some(Boolean)}
            className="h-10 px-4 bg-gradient-to-r from-amber-500 via-purple-500 to-cyan-500 hover:from-amber-600 hover:via-purple-600 hover:to-cyan-600 text-white font-medium"
          >
            {Object.values(loading).some(Boolean) ? (
              <Loader2 className="w-4 h-4 animate-spin mr-2" />
            ) : (
              <Zap className="w-4 h-4 mr-2" />
            )}
            Compare All
          </Button>
        </div>

        {/* Compact Search Buttons */}
        <div className="grid grid-cols-3 gap-2">
          <SearchButton method="bm25" />
          <SearchButton method="vector" />
          <SearchButton method="hybrid" />
        </div>

        {/* Comparison Stats - Shows evidence of hybrid superiority */}
        {results.hybrid.length > 0 && results.vector.length > 0 && results.bm25.length > 0 && (
          <ComparisonStats
            bm25Results={results.bm25}
            vectorResults={results.vector}
            hybridResults={results.hybrid}
          />
        )}

        {/* Results Grid - headers already in SearchButton row above */}
        <div className="grid grid-cols-3 gap-3 min-h-[250px]">
          <ResultsColumn method="bm25" />
          <ResultsColumn method="vector" />
          <ResultsColumn method="hybrid" />
        </div>
      </CardContent>

      <MethodExplainModal method={explainMethod} isOpen={explainMethod !== null} onClose={() => setExplainMethod(null)} />

      {selectedResult && (
        <ResultJourneyModal
          result={selectedResult}
          isOpen={isJourneyModalOpen}
          onClose={() => { setIsJourneyModalOpen(false); setSelectedResult(null); }}
        />
      )}
    </Card>
  );
};

export default HybridSearchPanel;
