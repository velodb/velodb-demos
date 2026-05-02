import { useState, memo } from "react";
import { FileText, Hash, Sparkles, ChevronDown, ChevronUp, Image, Table, Code, FileCode } from "lucide-react";
import { cn } from "@/lib/utils";

export type SearchType = "vector" | "bm25" | "hybrid";
export type ContentType = "text" | "image" | "table" | "code" | "formula";

// Cross-method score data for expanded view
export interface CrossMethodScores {
  vectorScore?: number;
  vectorRank?: number;
  bm25Score?: number;
  bm25Rank?: number;
  hybridScore?: number;
  hybridRank?: number;
}

export interface SearchResult {
  id: string;
  rank: number;
  score: number;
  text: string;
  documentName?: string;
  chunkIndex?: number;
  matchType: SearchType;
  contentType?: ContentType;  // NEW: content type for multimodal display
  highlightedText?: string;
  // F37: Expanded view fields
  crossMethodScores?: CrossMethodScores;
  matchedTerms?: string[];  // BM25 keyword matches
}

// Content type display config
const contentTypeConfig: Record<ContentType, { icon: typeof FileText; label: string; color: string; bgColor: string }> = {
  text: { icon: FileText, label: "Text", color: "text-slate-600", bgColor: "bg-slate-100" },
  image: { icon: Image, label: "Image", color: "text-green-600", bgColor: "bg-green-100" },
  table: { icon: Table, label: "Table", color: "text-blue-600", bgColor: "bg-blue-100" },
  code: { icon: Code, label: "Code", color: "text-orange-600", bgColor: "bg-orange-100" },
  formula: { icon: FileCode, label: "Formula", color: "text-pink-600", bgColor: "bg-pink-100" },
};

interface SearchResultCardProps {
  result: SearchResult;
  index: number;
  animationDelay?: number;
  isHighlighted?: boolean;
  onClick?: (result: SearchResult) => void;
  // F37: New props for expanded view
  expandable?: boolean;
  showRankComparison?: boolean;
}

const matchTypeConfig: Record<SearchType, { label: string; color: string; bgColor: string }> = {
  vector: {
    label: "Vector",
    color: "text-purple-600",
    bgColor: "bg-purple-100",
  },
  bm25: {
    label: "BM25",
    color: "text-amber-600",
    bgColor: "bg-amber-100",
  },
  hybrid: {
    label: "Hybrid",
    color: "text-cyan-600",
    bgColor: "bg-cyan-100",
  },
};

// Helper to render score bar
const ScoreBar = ({ score, color }: { score: number; color: string }) => (
  <div className="h-2 bg-muted rounded-full overflow-hidden flex-1">
    <div
      className={cn("h-full rounded-full transition-all duration-500", color)}
      style={{ width: `${Math.min(score * 100, 100)}%` }}
    />
  </div>
);

// Helper to render rank badge
const RankBadge = ({ rank, color }: { rank: number | undefined; color: string }) => {
  if (rank === undefined) return <span className="text-muted-foreground">-</span>;
  return (
    <span className={cn("font-mono text-xs font-bold", color)}>
      #{rank}
    </span>
  );
};

const SearchResultCard = memo(({
  result,
  index,
  animationDelay = 100,
  isHighlighted = false,
  onClick,
  expandable = false,
  showRankComparison = false,
}: SearchResultCardProps) => {
  const [isExpanded, setIsExpanded] = useState(false);

  const config = matchTypeConfig[result.matchType];
  const { crossMethodScores, matchedTerms } = result;

  const handleExpandToggle = (e: React.MouseEvent) => {
    e.stopPropagation();
    setIsExpanded(!isExpanded);
  };

  return (
    <div
      className={cn(
        "relative p-4 rounded-lg border bg-white transition-all duration-150 cursor-pointer",
        "hover:shadow-md hover:border-primary/50",
        isHighlighted && "ring-2 ring-primary ring-offset-2 shadow-lg"
      )}
      onClick={() => onClick?.(result)}
      data-testid={`search-result-${result.id}`}
    >
      {/* Rank Badge */}
      <div className="absolute -top-2 -left-2 w-6 h-6 rounded-full bg-primary text-white flex items-center justify-center text-xs font-bold shadow-md">
        {result.rank}
      </div>

      {/* Header Row */}
      <div className="flex items-center justify-between mb-2 pl-4">
        <div className="flex items-center gap-2">
          {/* Match Type Badge */}
          <span
            className={cn(
              "px-2 py-0.5 rounded-full text-xs font-medium",
              config.bgColor,
              config.color
            )}
          >
            {config.label}
          </span>
          {/* Content Type Badge (for multimodal) */}
          {result.contentType && result.contentType !== "text" && (() => {
            const ctConfig = contentTypeConfig[result.contentType];
            const ContentIcon = ctConfig.icon;
            return (
              <span
                className={cn(
                  "px-2 py-0.5 rounded-full text-xs font-medium flex items-center gap-1",
                  ctConfig.bgColor,
                  ctConfig.color
                )}
              >
                <ContentIcon className="w-3 h-3" />
                {ctConfig.label}
              </span>
            );
          })()}
        </div>

        {/* Score + Expand Toggle */}
        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1 text-sm text-muted-foreground">
            <Sparkles className="w-3 h-3" />
            <span className="font-mono">{result.score.toFixed(4)}</span>
          </div>
          {expandable && (
            <button
              onClick={handleExpandToggle}
              className="p-1 hover:bg-muted rounded transition-colors"
              data-testid={`expand-btn-${result.id}`}
            >
              {isExpanded ? (
                <ChevronUp className="w-4 h-4 text-muted-foreground" />
              ) : (
                <ChevronDown className="w-4 h-4 text-muted-foreground" />
              )}
            </button>
          )}
        </div>
      </div>

      {/* Text Content */}
      <p className={cn(
        "text-sm text-foreground mb-2",
        isExpanded ? "" : "line-clamp-3"
      )}>
        {result.highlightedText ? (
          <span dangerouslySetInnerHTML={{ __html: result.highlightedText }} />
        ) : (
          result.text
        )}
      </p>

      {/* Footer */}
      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <div className="flex items-center gap-4">
          {result.documentName && (
            <div className="flex items-center gap-1">
              <FileText className="w-3 h-3" />
              <span className="truncate max-w-[150px]">{result.documentName}</span>
            </div>
          )}
          {result.chunkIndex !== undefined && (
            <div className="flex items-center gap-1">
              <Hash className="w-3 h-3" />
              <span>Chunk {result.chunkIndex}</span>
            </div>
          )}
        </div>

        {/* F37: Rank Comparison Mini-Display */}
        {showRankComparison && crossMethodScores && (
          <div className="flex items-center gap-2 text-xs" data-testid={`rank-comparison-${result.id}`}>
            <span className="text-purple-600 font-mono">V{crossMethodScores.vectorRank ? `#${crossMethodScores.vectorRank}` : '-'}</span>
            <span className="text-amber-600 font-mono">B{crossMethodScores.bm25Rank ? `#${crossMethodScores.bm25Rank}` : '-'}</span>
            <span className="text-cyan-600 font-mono">H{crossMethodScores.hybridRank ? `#${crossMethodScores.hybridRank}` : '-'}</span>
          </div>
        )}
      </div>

      {/* F37: Expanded View - Score Breakdown & Matched Terms */}
      {isExpanded && (
        <div className="mt-4 pt-4 border-t space-y-4" data-testid={`expanded-section-${result.id}`}>
          {/* Score Breakdown */}
          {crossMethodScores && (
            <div className="space-y-2">
              <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Score Breakdown</h4>

              {/* Vector Score */}
              <div className="flex items-center gap-3">
                <span className="text-xs w-16 text-purple-600 font-medium">Vector</span>
                <ScoreBar score={crossMethodScores.vectorScore || 0} color="bg-purple-500" />
                <span className="text-xs font-mono w-12 text-right">
                  {crossMethodScores.vectorScore?.toFixed(2) || '-'}
                </span>
                <RankBadge rank={crossMethodScores.vectorRank} color="text-purple-600" />
              </div>

              {/* BM25 Score */}
              <div className="flex items-center gap-3">
                <span className="text-xs w-16 text-amber-600 font-medium">BM25</span>
                <ScoreBar score={crossMethodScores.bm25Score || 0} color="bg-amber-500" />
                <span className="text-xs font-mono w-12 text-right">
                  {crossMethodScores.bm25Score?.toFixed(2) || '-'}
                </span>
                <RankBadge rank={crossMethodScores.bm25Rank} color="text-amber-600" />
              </div>

              {/* Hybrid Score */}
              <div className="flex items-center gap-3">
                <span className="text-xs w-16 text-cyan-600 font-medium">Hybrid</span>
                <ScoreBar score={crossMethodScores.hybridScore || 0} color="bg-cyan-500" />
                <span className="text-xs font-mono w-12 text-right">
                  {crossMethodScores.hybridScore?.toFixed(2) || '-'}
                </span>
                <RankBadge rank={crossMethodScores.hybridRank} color="text-cyan-600" />
              </div>
            </div>
          )}

          {/* Matched Terms (BM25 Keywords) */}
          {matchedTerms && matchedTerms.length > 0 && (
            <div>
              <h4 className="text-xs font-semibold text-muted-foreground uppercase tracking-wide mb-2">Matched Terms (BM25)</h4>
              <div className="flex flex-wrap gap-1">
                {matchedTerms.map((term, i) => (
                  <span
                    key={i}
                    className="px-2 py-0.5 bg-amber-100 text-amber-700 rounded text-xs font-medium"
                  >
                    {term}
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
});

SearchResultCard.displayName = "SearchResultCard";

export default SearchResultCard;
