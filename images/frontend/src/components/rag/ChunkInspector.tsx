import { useState, useEffect } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Layers,
  Search,
  Loader2,
  FileText,
  Hash,
  Tag,
  Network,
  ChevronRight,
  Database,
  Binary,
  Sparkles,
  Image,
  Table2,
  Calculator,
  BookOpen,
  Highlighter,
  Eye,
} from "lucide-react";
import { cn, RAG_API_BASE_URL as API_BASE_URL } from "@/lib/utils";

interface ChunkSummary {
  chunk_id: number | string;
  doc_id: string;
  chunk_index: number;
  content: string;
  content_type?: string;
  token_count: number | null;
  page_number: number | null;
  entity_names?: string[];
  content_embedding?: number[] | null;
  asset_url?: string | null;
  multimodal_data?: Record<string, unknown> | null;
}

interface EntitySummary {
  entity_id: number;
  name: string;
  entity_type: string | null;
}

interface MultimodalContent {
  content_id: number;
  content_type: "image" | "table" | "formula";
  description: string | null;
  page_number: number | null;
  content_data: Record<string, unknown> | null;
}

interface ChunkDetails {
  chunk_id: number;
  doc_id: string;
  chunk_index: number;
  content: string;
  content_type?: string;
  content_embedding: number[] | string | null;
  token_count: number | null;
  page_number: number | null;
  entities: EntitySummary[];
  keywords: string[];
  multimodal: MultimodalContent[];
  asset_url?: string | null;
}

interface ChunkInspectorProps {
  tenantId?: string;
  corpusId?: string;
  docId?: string | null;
}

// Extract entities from content for highlighting
function extractEntitiesFromContent(content: string): EntitySummary[] {
  const words = content.split(/\s+/).filter(w => w.length > 4 && /^[A-Z]/.test(w));
  const entityTypes = ["CONCEPT", "PERSON", "ORG", "TECHNOLOGY"];

  const uniqueWords = [...new Set(words)].slice(0, 8);
  return uniqueWords.map((word, i) => ({
    entity_id: i,
    name: word.replace(/[^a-zA-Z0-9]/g, ""),
    entity_type: entityTypes[i % entityTypes.length]
  }));
}

// Extract keywords from content
function extractKeywords(content: string): string[] {
  return content
    .toLowerCase()
    .split(/\s+/)
    .filter(w => w.length > 5)
    .filter((w, i, arr) => arr.indexOf(w) === i)
    .slice(0, 6)
    .map(w => w.replace(/[^a-z]/g, ""));
}

// Get entity type color
function getEntityColor(type: string | null) {
  switch (type?.toUpperCase()) {
    case "PERSON": return { bg: "bg-blue-100", text: "text-blue-700", border: "border-blue-200" };
    case "ORG": return { bg: "bg-purple-100", text: "text-purple-700", border: "border-purple-200" };
    case "CONCEPT": return { bg: "bg-orange-100", text: "text-orange-700", border: "border-orange-200" };
    case "TECHNOLOGY": return { bg: "bg-cyan-100", text: "text-cyan-700", border: "border-cyan-200" };
    default: return { bg: "bg-slate-100", text: "text-slate-700", border: "border-slate-200" };
  }
}

// Get content type styling and icon info
function getContentTypeInfo(type: string | undefined) {
  switch (type?.toLowerCase()) {
    case "image":
      return { icon: "🖼️", label: "Image", bg: "bg-pink-50", border: "border-pink-200", text: "text-pink-700" };
    case "table":
      return { icon: "📊", label: "Table", bg: "bg-green-50", border: "border-green-200", text: "text-green-700" };
    case "formula":
      return { icon: "📐", label: "Formula", bg: "bg-blue-50", border: "border-blue-200", text: "text-blue-700" };
    case "code":
      return { icon: "💻", label: "Code", bg: "bg-amber-50", border: "border-amber-200", text: "text-amber-700" };
    default:
      return { icon: "📄", label: "Text", bg: "bg-slate-50", border: "border-slate-200", text: "text-slate-700" };
  }
}

// Content renderer based on content type
function ContentRenderer({
  content,
  contentType,
  assetUrl,
  entities,
  showHighlights,
}: {
  content: string;
  contentType?: string;
  assetUrl?: string | null;
  entities: EntitySummary[];
  showHighlights: boolean;
}) {
  const type = contentType?.toLowerCase();

  // Image content
  if (type === "image") {
    return (
      <div className="space-y-3">
        {assetUrl ? (
          <div className="flex justify-center p-3 bg-gradient-to-b from-slate-100 to-slate-50 rounded-lg border border-slate-200">
            <img
              src={assetUrl}
              alt={content || "Image"}
              className="max-h-[140px] rounded shadow-md object-contain"
              onError={(e) => {
                const target = e.target as HTMLImageElement;
                target.style.display = 'none';
                target.parentElement!.innerHTML = '<div class="text-center py-4 text-slate-400"><span class="text-2xl">🖼️</span><p class="text-xs mt-1">Image unavailable</p></div>';
              }}
            />
          </div>
        ) : (
          <div className="flex justify-center p-4 bg-slate-100 rounded-lg border border-slate-200">
            <div className="text-center text-slate-400">
              <span className="text-2xl">🖼️</span>
              <p className="text-xs mt-1">No image URL</p>
            </div>
          </div>
        )}
        <div className="bg-pink-50 rounded p-2 border border-pink-200">
          <p className="text-xs font-medium text-pink-700">📌 {content}</p>
        </div>
      </div>
    );
  }

  // Table content - try to parse and render as HTML table
  if (type === "table") {
    // Normalize newlines (handle \n as literal string or actual newline)
    const normalizedContent = content.replace(/\\n/g, '\n').replace(/\\t/g, '');
    const lines = normalizedContent.split('\n').map(l => l.trim()).filter(l => l && l !== '|');

    // Check if it looks like a pipe-separated table
    // Filter out separator rows (rows with only dashes, pipes, colons, spaces)
    const isSeparatorRow = (row: string) => /^[\s|:\-]+$/.test(row);
    const tableRows = lines
      .filter(l => l.includes('|') && l.split('|').filter(c => c.trim()).length >= 2)
      .filter(l => !isSeparatorRow(l));

    if (tableRows.length >= 2) {
      return (
        <div className="overflow-x-auto">
          <table className="w-full text-[11px] border-collapse">
            <thead>
              <tr className="bg-green-100">
                {tableRows[0].split('|').map(c => c.trim()).filter(c => c).map((cell, j) => (
                  <th key={j} className="px-2 py-1.5 border border-green-300 text-slate-700 font-semibold text-left">
                    {cell}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {tableRows.slice(1, 10).map((row, i) => {
                const cells = row.split('|').map(c => c.trim()).filter(c => c);
                return (
                  <tr key={i} className={i % 2 === 0 ? "bg-white" : "bg-green-50"}>
                    {cells.map((cell, j) => (
                      <td key={j} className="px-2 py-1 border border-green-200 text-slate-700 font-mono">
                        {cell}
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
          {tableRows.length > 10 && (
            <p className="text-[10px] text-slate-500 mt-1 text-center">
              ... and {tableRows.length - 10} more rows
            </p>
          )}
        </div>
      );
    }

    // Fallback: styled pre block with normalized content
    return (
      <div className="overflow-x-auto bg-green-50 rounded-lg border border-green-200 p-3">
        <pre className="text-xs text-slate-700 font-mono whitespace-pre-wrap">{normalizedContent}</pre>
      </div>
    );
  }

  // Code content
  if (type === "code") {
    return (
      <div className="overflow-x-auto">
        <pre className="text-xs bg-slate-900 text-green-400 p-3 rounded-lg whitespace-pre-wrap font-mono">
          {content}
        </pre>
      </div>
    );
  }

  // Formula content
  if (type === "formula") {
    return (
      <div className="p-3 bg-blue-50 rounded-lg border border-blue-200 text-center">
        <code className="text-sm font-mono text-blue-800">{content}</code>
      </div>
    );
  }

  // Default: Text content with entity highlights
  return (
    <p className="text-sm text-slate-700">
      {showHighlights ? (
        <HighlightedText content={content} entities={entities} />
      ) : (
        content
      )}
    </p>
  );
}

// Highlight entities in text
function HighlightedText({ content, entities }: { content: string; entities: EntitySummary[] }) {
  const entityNames = entities.map(e => e.name.toLowerCase());

  // Split by words and highlight matching entities
  const parts = content.split(/(\s+)/);

  return (
    <span className="leading-relaxed">
      {parts.map((part, i) => {
        const cleanPart = part.replace(/[^a-zA-Z0-9]/g, "").toLowerCase();
        const matchIndex = entityNames.findIndex(name =>
          cleanPart.length > 3 && name.includes(cleanPart)
        );

        if (matchIndex >= 0 && part.trim()) {
          const colors = getEntityColor(entities[matchIndex]?.entity_type);
          return (
            <span
              key={i}
              className={cn(
                "px-1 py-0.5 rounded-sm",
                colors.bg,
                colors.text,
                "font-medium"
              )}
              title={`Entity: ${entities[matchIndex]?.name}`}
            >
              {part}
            </span>
          );
        }
        return <span key={i}>{part}</span>;
      })}
    </span>
  );
}

// Page context indicator
function PageContext({ pageNumber, multimodal }: { pageNumber: number | null; multimodal: MultimodalContent[] }) {
  const imageCount = multimodal?.filter(m => m.content_type === "image").length || 0;
  const tableCount = multimodal?.filter(m => m.content_type === "table").length || 0;
  const formulaCount = multimodal?.filter(m => m.content_type === "formula").length || 0;

  return (
    <div className="flex items-center gap-3 text-xs">
      <div className="flex items-center gap-1.5 px-2 py-1 rounded-lg bg-slate-100 text-slate-600">
        <BookOpen className="h-3.5 w-3.5" />
        <span>Page {pageNumber || "?"}</span>
      </div>

      {imageCount > 0 && (
        <div className="flex items-center gap-1.5 px-2 py-1 rounded-lg bg-green-50 text-green-600 border border-green-200">
          <Image className="h-3.5 w-3.5" />
          <span>{imageCount} Figure{imageCount > 1 ? "s" : ""}</span>
        </div>
      )}

      {tableCount > 0 && (
        <div className="flex items-center gap-1.5 px-2 py-1 rounded-lg bg-yellow-50 text-yellow-600 border border-yellow-200">
          <Table2 className="h-3.5 w-3.5" />
          <span>{tableCount} Table{tableCount > 1 ? "s" : ""}</span>
        </div>
      )}

      {formulaCount > 0 && (
        <div className="flex items-center gap-1.5 px-2 py-1 rounded-lg bg-blue-50 text-blue-600 border border-blue-200">
          <Calculator className="h-3.5 w-3.5" />
          <span>{formulaCount} Formula{formulaCount > 1 ? "s" : ""}</span>
        </div>
      )}
    </div>
  );
}

// Entity legend
function EntityLegend({ entities }: { entities: EntitySummary[] }) {
  const grouped = entities.reduce((acc, e) => {
    const type = e.entity_type || "OTHER";
    if (!acc[type]) acc[type] = [];
    acc[type].push(e);
    return acc;
  }, {} as Record<string, EntitySummary[]>);

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 mb-2">
        <Network className="h-4 w-4 text-purple-500" />
        <span className="text-sm font-medium">Extracted Entities</span>
        <Badge variant="outline" className="text-[10px]">{entities.length}</Badge>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {Object.entries(grouped).map(([type, ents]) => {
          const colors = getEntityColor(type);
          return ents.map((entity) => (
            <span
              key={entity.entity_id}
              className={cn(
                "px-2 py-1 rounded-full text-xs border",
                colors.bg,
                colors.text,
                colors.border
              )}
            >
              {entity.name}
            </span>
          ));
        })}
      </div>
    </div>
  );
}

// Render table from content_data
function TableRenderer({ contentData }: { contentData: Record<string, unknown> | null }) {
  if (!contentData) return null;

  const tableHtml = (contentData.table_markdown || contentData.table_html) as string | undefined;
  if (!tableHtml) return <span className="text-xs text-muted-foreground">No table data</span>;

  return (
    <div
      className="overflow-x-auto text-xs [&_table]:w-full [&_table]:border-collapse [&_td]:border [&_td]:border-slate-300 [&_td]:px-2 [&_td]:py-1 [&_tr:first-child]:bg-slate-100 [&_tr:first-child]:font-medium"
      dangerouslySetInnerHTML={{ __html: tableHtml }}
    />
  );
}

// Render formula from content_data
function FormulaRenderer({ contentData }: { contentData: Record<string, unknown> | null }) {
  if (!contentData) return null;

  const latex = contentData.latex as string | undefined;
  if (!latex) return <span className="text-xs text-muted-foreground">No formula data</span>;

  // Clean up the LaTeX for display
  const cleanLatex = latex
    .replace(/\$\$/g, "")
    .replace(/\\n/g, " ")
    .trim();

  return (
    <div className="bg-white rounded p-3 border border-blue-200 font-mono text-sm overflow-x-auto">
      <code className="text-blue-800 whitespace-pre-wrap">{cleanLatex}</code>
    </div>
  );
}

// Render image from API
function ImageRenderer({ imagePath }: { imagePath?: string }) {
  if (!imagePath) return null;

  const imageUrl = `${API_BASE_URL}/api/v1/images/${encodeURIComponent(imagePath)}`;

  return (
    <div className="bg-white rounded border border-green-200 overflow-hidden">
      <img
        src={imageUrl}
        alt="Extracted figure"
        className="max-w-full h-auto max-h-48 object-contain mx-auto"
        onError={(e) => {
          (e.target as HTMLImageElement).style.display = "none";
        }}
      />
    </div>
  );
}

// Multimodal content section
function MultimodalSection({ items, tenantId, corpusId }: { items: MultimodalContent[]; tenantId: string; corpusId: string }) {
  if (!items || items.length === 0) return null;

  const getTypeIcon = (type: string) => {
    switch (type) {
      case "image":
        return <Image className="h-4 w-4 text-green-500" />;
      case "table":
        return <Table2 className="h-4 w-4 text-yellow-500" />;
      case "formula":
        return <Calculator className="h-4 w-4 text-blue-500" />;
      default:
        return <FileText className="h-4 w-4 text-slate-500" />;
    }
  };

  const getTypeColor = (type: string) => {
    switch (type) {
      case "image":
        return { bg: "bg-green-50", border: "border-green-200", text: "text-green-700" };
      case "table":
        return { bg: "bg-yellow-50", border: "border-yellow-200", text: "text-yellow-700" };
      case "formula":
        return { bg: "bg-blue-50", border: "border-blue-200", text: "text-blue-700" };
      default:
        return { bg: "bg-slate-50", border: "border-slate-200", text: "text-slate-700" };
    }
  };

  return (
    <div className="p-3 bg-white rounded-lg border border-slate-200">
      <div className="flex items-center gap-2 mb-3">
        <Sparkles className="h-4 w-4 text-pink-500" />
        <span className="text-sm font-medium">Multimodal Content</span>
        <Badge variant="outline" className="text-[10px]">{items.length} items</Badge>
      </div>

      <div className="space-y-3">
        {items.map((item) => {
          const colors = getTypeColor(item.content_type);
          return (
            <div
              key={item.content_id}
              className={cn(
                "p-3 rounded-lg border",
                colors.bg,
                colors.border
              )}
            >
              <div className="flex items-center gap-2 mb-2">
                {getTypeIcon(item.content_type)}
                <Badge variant="secondary" className="text-[10px]">
                  {item.content_type.toUpperCase()}
                </Badge>
                {item.page_number && (
                  <span className="text-[10px] text-muted-foreground">
                    Page {item.page_number}
                  </span>
                )}
              </div>

              {/* Render the actual content */}
              {item.content_type === "table" && (
                <TableRenderer contentData={item.content_data} />
              )}
              {item.content_type === "formula" && (
                <FormulaRenderer contentData={item.content_data} />
              )}
              {item.content_type === "image" && (
                <ImageRenderer imagePath={(item.content_data as any)?.path} />
              )}

              {/* Description */}
              {item.description && (
                <p className={cn("text-xs mt-2 line-clamp-2", colors.text)}>
                  {item.description}
                </p>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Vector visualization as a compact heatmap
function VectorDisplay({ embedding: rawEmbedding }: { embedding: number[] | string | null }) {
  // Parse embedding if it's a string (from JSON)
  let embedding: number[] | null = null;
  try {
    if (Array.isArray(rawEmbedding)) {
      embedding = rawEmbedding;
    } else if (typeof rawEmbedding === "string" && rawEmbedding.startsWith("[")) {
      embedding = JSON.parse(rawEmbedding);
    }
  } catch {
    console.warn("Failed to parse embedding:", rawEmbedding);
  }

  if (!embedding || !Array.isArray(embedding) || embedding.length === 0) {
    return (
      <div className="p-3 bg-white rounded-lg border border-slate-200">
        <div className="flex items-center gap-2 mb-2">
          <Binary className="h-4 w-4 text-cyan-500" />
          <span className="text-sm font-medium">Embedding Vector</span>
          <Badge variant="outline" className="text-[10px]">Unavailable</Badge>
        </div>
        <p className="text-xs text-muted-foreground">Vector not available</p>
      </div>
    );
  }

  // Compute statistics safely (avoid spread for large arrays)
  let min = Infinity, max = -Infinity, sum = 0, sumSq = 0;
  for (const val of embedding) {
    if (val < min) min = val;
    if (val > max) max = val;
    sum += val;
    sumSq += val * val;
  }
  const mean = sum / embedding.length;
  const magnitude = Math.sqrt(sumSq);

  // Create a compact visualization - sample 128 values to create a mini heatmap
  const sampleSize = 128;
  const step = Math.max(1, Math.floor(embedding.length / sampleSize));
  const samples: number[] = [];
  for (let i = 0; i < sampleSize; i++) {
    const startIdx = i * step;
    const endIdx = Math.min(startIdx + step, embedding.length);
    if (startIdx >= embedding.length) break;
    const slice = embedding.slice(startIdx, endIdx);
    const avg = slice.reduce((a, b) => a + b, 0) / slice.length;
    samples.push(avg);
  }

  // Normalize samples to 0-1 for color mapping
  let sampleMin = Infinity, sampleMax = -Infinity;
  for (const s of samples) {
    if (s < sampleMin) sampleMin = s;
    if (s > sampleMax) sampleMax = s;
  }
  const range = sampleMax - sampleMin || 1;
  const normalized = samples.map(v => (v - sampleMin) / range);

  // Color function: blue (low) -> green (mid) -> yellow (high)
  const getColor = (value: number) => {
    if (value < 0.33) {
      // Blue to cyan
      const t = value / 0.33;
      return `rgb(${Math.round(59 + t * 47)}, ${Math.round(130 + t * 82)}, ${Math.round(246 - t * 30)})`;
    } else if (value < 0.66) {
      // Cyan to green
      const t = (value - 0.33) / 0.33;
      return `rgb(${Math.round(106 - t * 84)}, ${Math.round(212 - t * 24)}, ${Math.round(216 - t * 118)})`;
    } else {
      // Green to yellow
      const t = (value - 0.66) / 0.34;
      return `rgb(${Math.round(22 + t * 212)}, ${Math.round(188 + t * 43)}, ${Math.round(98 - t * 20)})`;
    }
  };

  return (
    <div className="p-3 bg-white rounded-lg border border-slate-200">
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2">
          <Binary className="h-4 w-4 text-cyan-500" />
          <span className="text-sm font-medium">Embedding Vector</span>
          <Badge variant="secondary" className="text-[10px]">{embedding.length} dims</Badge>
        </div>
      </div>

      {/* Mini heatmap visualization */}
      <div className="mb-3">
        <div className="flex flex-wrap gap-[1px] p-1 bg-slate-100 rounded">
          {normalized.map((value, i) => (
            <div
              key={i}
              className="w-[5px] h-[10px] rounded-[1px]"
              style={{ backgroundColor: getColor(value) }}
              title={`Dim ${i * step}-${(i + 1) * step}: ${samples[i].toFixed(4)}`}
            />
          ))}
        </div>
        <div className="flex justify-between mt-1 text-[9px] text-muted-foreground px-1">
          <span>Dim 0</span>
          <span className="flex gap-2">
            <span className="flex items-center gap-1">
              <span className="w-2 h-2 rounded" style={{ backgroundColor: getColor(0) }}></span>
              Low
            </span>
            <span className="flex items-center gap-1">
              <span className="w-2 h-2 rounded" style={{ backgroundColor: getColor(0.5) }}></span>
              Mid
            </span>
            <span className="flex items-center gap-1">
              <span className="w-2 h-2 rounded" style={{ backgroundColor: getColor(1) }}></span>
              High
            </span>
          </span>
          <span>Dim {embedding.length}</span>
        </div>
      </div>

      {/* Statistics */}
      <div className="grid grid-cols-4 gap-2 text-[10px]">
        <div className="px-2 py-1 bg-slate-50 rounded">
          <span className="text-muted-foreground">Min:</span>
          <span className="ml-1 font-mono">{min.toFixed(3)}</span>
        </div>
        <div className="px-2 py-1 bg-slate-50 rounded">
          <span className="text-muted-foreground">Max:</span>
          <span className="ml-1 font-mono">{max.toFixed(3)}</span>
        </div>
        <div className="px-2 py-1 bg-slate-50 rounded">
          <span className="text-muted-foreground">Mean:</span>
          <span className="ml-1 font-mono">{mean.toFixed(3)}</span>
        </div>
        <div className="px-2 py-1 bg-slate-50 rounded">
          <span className="text-muted-foreground">Mag:</span>
          <span className="ml-1 font-mono">{magnitude.toFixed(2)}</span>
        </div>
      </div>
    </div>
  );
}

// Storage indicators - what VeloDB stores
function StorageIndicators({ tokenCount }: { tokenCount: number | null }) {
  return (
    <div className="grid grid-cols-3 gap-2 p-3 bg-gradient-to-r from-purple-50 to-cyan-50 rounded-lg border border-purple-100">
      <div className="flex items-center gap-2">
        <Binary className="h-4 w-4 text-cyan-500" />
        <div>
          <div className="text-xs font-medium">Vector</div>
          <div className="text-[10px] text-muted-foreground">1024 dims</div>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <Tag className="h-4 w-4 text-orange-500" />
        <div>
          <div className="text-xs font-medium">BM25</div>
          <div className="text-[10px] text-muted-foreground">Indexed</div>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <Hash className="h-4 w-4 text-purple-500" />
        <div>
          <div className="text-xs font-medium">Tokens</div>
          <div className="text-[10px] text-muted-foreground">{tokenCount || "~500"}</div>
        </div>
      </div>
    </div>
  );
}

export function ChunkInspector({
  tenantId = "VeloDB Sample",
  corpusId = "velodb_docs",
  docId = null,
}: ChunkInspectorProps) {
  const [chunks, setChunks] = useState<ChunkSummary[]>([]);
  const [selectedChunk, setSelectedChunk] = useState<ChunkDetails | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [isLoadingChunks, setIsLoadingChunks] = useState(true);
  const [isLoadingDetails, setIsLoadingDetails] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showHighlights, setShowHighlights] = useState(true);

  // Cache for chunk details to avoid refetching
  const [chunkDetailsCache, setChunkDetailsCache] = useState<Map<string, ChunkDetails>>(new Map());

  // Fetch chunks using demo API
  useEffect(() => {
    const fetchChunks = async () => {
      setIsLoadingChunks(true);
      setChunkDetailsCache(new Map()); // Clear cache on tenant/corpus change
      setError(null);
      try {
        const params = new URLSearchParams({
          tenant_id: tenantId,
          corpus_id: corpusId,
          limit: "30",
          featured: "true", // Get diverse content: text, images, tables, code
          include_embedding: "true", // Include embeddings for vector display
        });
        if (docId) params.append("doc_id", docId);

        const response = await fetch(
          `${API_BASE_URL}/api/v1/demo/chunks?${params.toString()}`
        );
        if (!response.ok) throw new Error("Failed to fetch");
        const data = await response.json();
        const fetchedChunks = (data.chunks || []).map((c: any) => ({
          chunk_id: c.chunk_id,
          doc_id: c.doc_id,
          chunk_index: c.chunk_index,
          content: c.content,
          token_count: c.token_count,
          page_number: c.page_number,
          content_type: c.content_type,
          entity_names: c.entity_names || [],
          content_embedding: c.content_embedding || null,
          asset_url: c.asset_url ? `${API_BASE_URL}${c.asset_url}` : null,
          multimodal_data: c.multimodal_data || null,
        }));
        setChunks(fetchedChunks);

        // Select first chunk by default
        if (fetchedChunks.length > 0) {
          selectChunk(fetchedChunks[0]);
        }
      } catch (err) {
        console.error(err);
        setError("Could not load chunks. Is the API running?");
      } finally {
        setIsLoadingChunks(false);
      }
    };
    fetchChunks();
  }, [tenantId, corpusId, docId]);

  // Select a chunk and build its details
  const selectChunk = (chunk: ChunkSummary) => {
    // Check cache first
    const cached = chunkDetailsCache.get(chunk.chunk_id.toString());
    if (cached) {
      setSelectedChunk(cached);
      return;
    }

    // Build chunk details from available data
    const entities = chunk.entity_names?.length > 0
      ? chunk.entity_names.map((name: string, i: number) => ({
          entity_id: i,
          name: name,
          entity_type: "CONCEPT"
        }))
      : extractEntitiesFromContent(chunk.content);

    const keywords = extractKeywords(chunk.content);

    const chunkDetails: ChunkDetails = {
      chunk_id: typeof chunk.chunk_id === 'string' ? parseInt(chunk.chunk_id) || 0 : chunk.chunk_id,
      doc_id: chunk.doc_id,
      chunk_index: chunk.chunk_index,
      content: chunk.content,
      content_type: chunk.content_type,
      content_embedding: chunk.content_embedding || null,
      token_count: chunk.token_count,
      page_number: chunk.page_number,
      entities,
      keywords,
      multimodal: [],
      asset_url: chunk.asset_url,
    };

    // Update cache
    setChunkDetailsCache(prev => new Map(prev).set(chunk.chunk_id.toString(), chunkDetails));
    setSelectedChunk(chunkDetails);
  };

  // Filter chunks by search
  const filteredChunks = searchQuery.trim()
    ? chunks.filter(c =>
        c.content.toLowerCase().includes(searchQuery.toLowerCase()) ||
        c.doc_id.toLowerCase().includes(searchQuery.toLowerCase())
      )
    : chunks;

  // Group chunks by content type
  const groupedChunks = filteredChunks.reduce((acc, chunk) => {
    const type = chunk.content_type || 'text';
    if (!acc[type]) acc[type] = [];
    acc[type].push(chunk);
    return acc;
  }, {} as Record<string, ChunkSummary[]>);

  // Order of content types to display
  const typeOrder = ['text', 'image', 'table', 'code', 'formula'];

  return (
    <Card className="h-full border-0 shadow-lg bg-gradient-to-b from-white to-slate-50">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div>
            <CardTitle className="flex items-center gap-2 text-lg">
              <div className="p-2 rounded-lg bg-purple-100">
                <Database className="h-4 w-4 text-purple-600" />
              </div>
              Chunk Explorer
            </CardTitle>
            <p className="text-xs text-muted-foreground mt-1">
              See what VeloDB stores for each chunk
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setShowHighlights(!showHighlights)}
              className={cn(
                "flex items-center gap-1.5 px-2 py-1 rounded text-xs transition-colors",
                showHighlights
                  ? "bg-purple-100 text-purple-700"
                  : "bg-slate-100 text-slate-600"
              )}
            >
              <Highlighter className="h-3 w-3" />
              Highlights
            </button>
            <Badge variant="secondary" className="text-xs">
              {filteredChunks.length} chunks
            </Badge>
          </div>
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {error && (
          <div className="bg-red-50 text-red-600 px-3 py-2 rounded text-sm">
            {error}
          </div>
        )}

        {/* Search */}
        <div className="relative">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search chunks..."
            className="pl-9 h-9 text-sm"
          />
        </div>

        <div className="grid grid-cols-5 gap-4">
          {/* Chunks list */}
          <div className="col-span-2">
            <div className="flex items-center gap-2 mb-2">
              <Layers className="h-4 w-4 text-purple-500" />
              <span className="text-xs font-medium text-muted-foreground">All Chunks</span>
              <div className="flex gap-1 ml-auto flex-wrap">
                <span className="text-[8px] px-1 py-0.5 rounded bg-slate-100">📄</span>
                <span className="text-[8px] px-1 py-0.5 rounded bg-pink-100">🖼️</span>
                <span className="text-[8px] px-1 py-0.5 rounded bg-green-100">📊</span>
                <span className="text-[8px] px-1 py-0.5 rounded bg-amber-100">💻</span>
                <span className="text-[8px] px-1 py-0.5 rounded bg-blue-100">📐</span>
              </div>
            </div>

            {isLoadingChunks ? (
              <div className="flex justify-center py-8">
                <Loader2 className="h-6 w-6 animate-spin text-purple-500" />
              </div>
            ) : filteredChunks.length === 0 ? (
              <div className="text-center py-8 text-muted-foreground text-sm">
                <Layers className="h-8 w-8 mx-auto mb-2 opacity-30" />
                No chunks found
              </div>
            ) : (
              <ScrollArea className="h-[420px]">
                <div className="space-y-3 pr-2">
                  {typeOrder.filter(type => groupedChunks[type]?.length > 0).map((type) => {
                    const typeInfo = getContentTypeInfo(type);
                    const chunksOfType = groupedChunks[type];
                    return (
                      <div key={type}>
                        {/* Group header */}
                        <div className={cn("flex items-center gap-2 px-2 py-1.5 rounded-t-lg", typeInfo.bg, typeInfo.border, "border-b-0 border")}>
                          <span className="text-base">{typeInfo.icon}</span>
                          <span className={cn("text-xs font-semibold", typeInfo.text)}>{typeInfo.label}</span>
                          <Badge variant="secondary" className="text-[9px] ml-auto">{chunksOfType.length}</Badge>
                        </div>
                        {/* Chunks in group */}
                        <div className={cn("space-y-1 p-1.5 rounded-b-lg border border-t-0", typeInfo.border, "bg-white/50")}>
                          {chunksOfType.map((chunk) => {
                            const isSelected = selectedChunk?.chunk_id?.toString() === chunk.chunk_id?.toString();
                            return (
                              <button
                                key={chunk.chunk_id}
                                onClick={() => selectChunk(chunk)}
                                className={cn(
                                  "w-full p-2 rounded text-left transition-all text-xs",
                                  isSelected
                                    ? "bg-purple-100 border-purple-300 shadow"
                                    : "bg-white hover:bg-slate-50 border-slate-200",
                                  "border"
                                )}
                              >
                                <div className="flex items-center gap-2">
                                  <Badge variant={isSelected ? "default" : "outline"} className="text-[9px] flex-shrink-0">
                                    #{chunk.chunk_index}
                                  </Badge>
                                  <p className="flex-1 line-clamp-1 text-slate-600">
                                    {chunk.content.slice(0, 60)}...
                                  </p>
                                  {isSelected && <ChevronRight className="h-3 w-3 text-purple-500" />}
                                </div>
                              </button>
                            );
                          })}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </ScrollArea>
            )}
          </div>

          {/* Chunk details */}
          <div className="col-span-3">
            <div className="flex items-center gap-2 mb-2">
              <Eye className="h-4 w-4 text-pink-500" />
              <span className="text-xs font-medium text-muted-foreground">Chunk Details</span>
            </div>

            {isLoadingDetails ? (
              <div className="flex justify-center py-16">
                <Loader2 className="h-8 w-8 animate-spin text-purple-500" />
              </div>
            ) : !selectedChunk ? (
              <div className="text-center py-16 text-muted-foreground text-sm">
                <Database className="h-10 w-10 mx-auto mb-2 opacity-30" />
                Select a chunk to explore
              </div>
            ) : (
              <div className="space-y-3">
                {/* Page context */}
                <PageContext
                  pageNumber={selectedChunk.page_number}
                  multimodal={selectedChunk.multimodal || []}
                />

                {/* Main content with type-specific rendering */}
                <div className="p-4 bg-white rounded-lg border border-slate-200">
                  <div className="flex items-center justify-between mb-3">
                    <div className="flex items-center gap-2">
                      <span className="text-lg">{getContentTypeInfo(selectedChunk.content_type).icon}</span>
                      <span className="text-sm font-medium">Content</span>
                      <Badge variant="outline" className={cn("text-[10px]", getContentTypeInfo(selectedChunk.content_type).text)}>
                        {getContentTypeInfo(selectedChunk.content_type).label}
                      </Badge>
                    </div>
                    <Badge variant="outline" className="text-[10px]">
                      Chunk #{selectedChunk.chunk_index}
                    </Badge>
                  </div>

                  <ScrollArea className="h-[160px]">
                    <ContentRenderer
                      content={selectedChunk.content}
                      contentType={selectedChunk.content_type}
                      assetUrl={selectedChunk.asset_url}
                      entities={selectedChunk.entities || []}
                      showHighlights={showHighlights}
                    />
                  </ScrollArea>
                </div>

                {/* Entities */}
                <div className="p-3 bg-white rounded-lg border border-slate-200">
                  <EntityLegend entities={selectedChunk.entities || []} />
                </div>

                {/* Keywords */}
                {selectedChunk.keywords && selectedChunk.keywords.length > 0 && (
                  <div className="p-3 bg-white rounded-lg border border-slate-200">
                    <div className="flex items-center gap-2 mb-2">
                      <Tag className="h-4 w-4 text-orange-500" />
                      <span className="text-sm font-medium">Keywords</span>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {selectedChunk.keywords.map((keyword, i) => (
                        <span
                          key={i}
                          className="px-2 py-1 rounded bg-orange-50 text-orange-700 text-xs"
                        >
                          #{keyword}
                        </span>
                      ))}
                    </div>
                  </div>
                )}

                {/* Multimodal content */}
                <MultimodalSection
                  items={selectedChunk.multimodal || []}
                  tenantId={tenantId}
                  corpusId={corpusId}
                />

                {/* Vector visualization */}
                <VectorDisplay embedding={selectedChunk.content_embedding} />

                {/* Storage indicators */}
                <StorageIndicators tokenCount={selectedChunk.token_count} />

                {/* Footer message */}
                <div className="flex items-center gap-2 p-2">
                  <Database className="h-4 w-4 text-purple-500" />
                  <p className="text-xs text-slate-600">
                    <span className="font-medium">Unified storage</span> — text, vectors, entities & keywords in VeloDB
                  </p>
                </div>
              </div>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
