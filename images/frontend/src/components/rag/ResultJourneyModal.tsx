import { X, FileText, Hash, Sparkles, Zap, ArrowRight, Lightbulb } from "lucide-react";
import { cn } from "@/lib/utils";
import { SearchResult, CrossMethodScores } from "./SearchResultCard";

interface ResultJourneyModalProps {
  result: SearchResult;
  isOpen: boolean;
  onClose: () => void;
}

// Helper to get insight text based on score comparison
const getVectorInsight = (scores: CrossMethodScores): string => {
  const { vectorScore = 0, bm25Score = 0 } = scores;
  if (vectorScore > bm25Score + 0.15) {
    return "Strong semantic match - content captures meaning without exact keywords";
  }
  if (vectorScore > bm25Score) {
    return "Moderate semantic relevance to query concepts";
  }
  return "Semantic similarity present but not dominant signal";
};

const getBm25Insight = (scores: CrossMethodScores): string => {
  const { vectorScore = 0, bm25Score = 0 } = scores;
  if (bm25Score > vectorScore + 0.15) {
    return "Strong keyword match - exact query terms found in content";
  }
  if (bm25Score > vectorScore) {
    return "Good keyword overlap with query terms";
  }
  if (bm25Score < 0.7) {
    return "Missing some exact query keywords, lower term frequency";
  }
  return "Moderate keyword match based on term frequency";
};

const getHybridInsight = (scores: CrossMethodScores): string => {
  const { vectorRank = 99, bm25Rank = 99, hybridRank = 99 } = scores;

  // Check if hybrid improved vs both
  if (hybridRank < vectorRank && hybridRank < bm25Rank) {
    return "RRF fusion boosted this result - strong in both semantic AND keyword signals";
  }
  // Check if hybrid is between the two
  if ((hybridRank > vectorRank && hybridRank < bm25Rank) ||
      (hybridRank < vectorRank && hybridRank > bm25Rank)) {
    return "RRF balanced the ranking between Vector and BM25 signals";
  }
  // Same as best
  if (hybridRank === vectorRank || hybridRank === bm25Rank) {
    return "Maintained top ranking - dominant signal carried through fusion";
  }
  return "RRF combined scores using reciprocal rank formula";
};

// Calculate RRF score for display
const calculateRRF = (vectorRank?: number, bm25Rank?: number, k = 60): string => {
  const vRank = vectorRank ?? 1000;
  const bRank = bm25Rank ?? 1000;
  const vectorContrib = 1 / (k + vRank);
  const bm25Contrib = 1 / (k + bRank);
  const total = vectorContrib + bm25Contrib;
  return `1/(${k}+${vRank}) + 1/(${k}+${bRank}) = ${total.toFixed(4)}`;
};

// Movement indicator component
const MovementIndicator = ({ from, to }: { from?: number; to?: number }) => {
  if (from === undefined || to === undefined) return null;
  const delta = from - to; // positive = improved (lower rank is better)

  if (delta > 0) {
    return (
      <span className="flex items-center gap-0.5 text-green-600 font-medium">
        <span>↑</span>
        <span>{delta}</span>
      </span>
    );
  }
  if (delta < 0) {
    return (
      <span className="flex items-center gap-0.5 text-red-600 font-medium">
        <span>↓</span>
        <span>{Math.abs(delta)}</span>
      </span>
    );
  }
  return <span className="text-gray-400">=</span>;
};

const ResultJourneyModal = ({ result, isOpen, onClose }: ResultJourneyModalProps) => {
  if (!isOpen) return null;

  const scores = result.crossMethodScores;
  const hasScores = scores && (scores.vectorScore !== undefined || scores.bm25Score !== undefined);

  // Get display values (with defaults for missing data)
  const vectorScore = scores?.vectorScore;
  const vectorRank = scores?.vectorRank;
  const bm25Score = scores?.bm25Score;
  const bm25Rank = scores?.bm25Rank;
  const hybridScore = scores?.hybridScore;
  const hybridRank = scores?.hybridRank;

  // Handle click outside to close
  const handleBackdropClick = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget) {
      onClose();
    }
  };

  // Handle escape key
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      onClose();
    }
  };

  return (
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4"
      onClick={handleBackdropClick}
      onKeyDown={handleKeyDown}
      role="dialog"
      aria-modal="true"
      aria-labelledby="journey-modal-title"
      tabIndex={-1}
      data-testid="result-journey-modal"
    >
      <div
        className="bg-white rounded-xl shadow-2xl max-w-2xl w-full max-h-[90vh] overflow-y-auto animate-in fade-in zoom-in-95 duration-200"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b bg-gradient-to-r from-slate-50 to-white">
          <div className="flex items-center gap-3">
            <div className="p-2 bg-cyan-100 rounded-lg">
              <FileText className="w-5 h-5 text-cyan-600" />
            </div>
            <div>
              <h2 id="journey-modal-title" className="font-semibold text-lg">
                {result.documentName || "Document"}
              </h2>
              <div className="flex items-center gap-1 text-sm text-muted-foreground">
                <Hash className="w-3 h-3" />
                <span>Chunk {result.chunkIndex}</span>
              </div>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-2 hover:bg-muted rounded-lg transition-colors"
            aria-label="Close modal"
            data-testid="close-modal-button"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content */}
        <div className="p-6 space-y-6">
          {/* Content Preview - Always show first */}
          <div>
            <h3 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-2">
              Content
            </h3>
            <div className="bg-slate-50 border rounded-lg p-4 text-sm text-foreground leading-relaxed whitespace-pre-wrap max-h-[300px] overflow-y-auto">
              {result.text}
            </div>
            {/* Content type and score */}
            <div className="flex items-center gap-4 mt-2 text-xs text-muted-foreground">
              <span className="flex items-center gap-1">
                <FileText className="w-3 h-3" />
                Type: {result.contentType || "text"}
              </span>
              <span className="flex items-center gap-1">
                <Sparkles className="w-3 h-3" />
                Score: {result.score?.toFixed(4) || "-"}
              </span>
            </div>
          </div>

          {/* Ranking Journey Visualization - Only if scores available */}
          {hasScores && (
          <>
          <div>
            <h3 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-4">
              Ranking Journey
            </h3>

            <div className="flex items-center justify-between gap-4">
              {/* Vector Rank Card */}
              <div className="flex-1 text-center">
                <div className={cn(
                  "p-4 rounded-xl border-2 transition-all",
                  "bg-purple-50 border-purple-200"
                )}>
                  <div className="flex items-center justify-center gap-1 mb-1">
                    <Sparkles className="w-4 h-4 text-purple-600" />
                    <span className="text-xs font-medium text-purple-600">Vector</span>
                  </div>
                  <div className="text-3xl font-bold text-purple-700">
                    #{vectorRank ?? "-"}
                  </div>
                  <div className="text-sm font-mono text-purple-600 mt-1">
                    {vectorScore?.toFixed(4) ?? "-"}
                  </div>
                </div>
                <div className="mt-2 text-xs text-muted-foreground max-w-[120px] mx-auto">
                  {getVectorInsight(scores).split(" - ")[0]}
                </div>
              </div>

              {/* Arrow 1 */}
              <div className="flex flex-col items-center">
                <ArrowRight className="w-6 h-6 text-gray-300" />
                <MovementIndicator from={vectorRank} to={bm25Rank} />
              </div>

              {/* BM25 Rank Card */}
              <div className="flex-1 text-center">
                <div className={cn(
                  "p-4 rounded-xl border-2 transition-all",
                  "bg-amber-50 border-amber-200"
                )}>
                  <div className="flex items-center justify-center gap-1 mb-1">
                    <FileText className="w-4 h-4 text-amber-600" />
                    <span className="text-xs font-medium text-amber-600">BM25</span>
                  </div>
                  <div className="text-3xl font-bold text-amber-700">
                    #{bm25Rank ?? "-"}
                  </div>
                  <div className="text-sm font-mono text-amber-600 mt-1">
                    {bm25Score?.toFixed(4) ?? "-"}
                  </div>
                </div>
                <div className="mt-2 text-xs text-muted-foreground max-w-[120px] mx-auto">
                  {getBm25Insight(scores).split(" - ")[0]}
                </div>
              </div>

              {/* Arrow 2 */}
              <div className="flex flex-col items-center">
                <ArrowRight className="w-6 h-6 text-gray-300" />
                <MovementIndicator from={bm25Rank} to={hybridRank} />
              </div>

              {/* Hybrid Rank Card */}
              <div className="flex-1 text-center">
                <div className={cn(
                  "p-4 rounded-xl border-2 transition-all",
                  "bg-cyan-50 border-cyan-200 ring-2 ring-cyan-300 ring-offset-2"
                )}>
                  <div className="flex items-center justify-center gap-1 mb-1">
                    <Zap className="w-4 h-4 text-cyan-600" />
                    <span className="text-xs font-medium text-cyan-600">Hybrid</span>
                  </div>
                  <div className="text-3xl font-bold text-cyan-700">
                    #{hybridRank ?? "-"}
                  </div>
                  <div className="text-sm font-mono text-cyan-600 mt-1">
                    {hybridScore?.toFixed(4) ?? "-"}
                  </div>
                </div>
                <div className="mt-2 text-xs text-muted-foreground max-w-[120px] mx-auto">
                  {getHybridInsight(scores).split(" - ")[0]}
                </div>
              </div>
            </div>
          </div>

          {/* Why The Difference? Section */}
          <div className="bg-slate-50 rounded-xl p-4 border">
            <div className="flex items-center gap-2 mb-3">
              <Lightbulb className="w-4 h-4 text-amber-500" />
              <h3 className="font-semibold text-sm">Why The Difference?</h3>
            </div>

            <ul className="space-y-2 text-sm">
              <li className="flex items-start gap-2">
                <span className="w-5 h-5 rounded-full bg-purple-100 text-purple-600 flex items-center justify-center text-xs font-bold shrink-0">V</span>
                <span>
                  <span className="font-medium text-purple-700">Vector (#{vectorRank}):</span>{" "}
                  <span className="text-muted-foreground">{getVectorInsight(scores)}</span>
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="w-5 h-5 rounded-full bg-amber-100 text-amber-600 flex items-center justify-center text-xs font-bold shrink-0">B</span>
                <span>
                  <span className="font-medium text-amber-700">BM25 (#{bm25Rank}):</span>{" "}
                  <span className="text-muted-foreground">{getBm25Insight(scores)}</span>
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="w-5 h-5 rounded-full bg-cyan-100 text-cyan-600 flex items-center justify-center text-xs font-bold shrink-0">H</span>
                <span>
                  <span className="font-medium text-cyan-700">Hybrid (#{hybridRank}):</span>{" "}
                  <span className="text-muted-foreground">{getHybridInsight(scores)}</span>
                </span>
              </li>
            </ul>

            {/* RRF Formula */}
            <div className="mt-4 pt-3 border-t">
              <p className="text-xs text-muted-foreground">
                <span className="font-medium">Weighted Score:</span>{" "}
                <code className="bg-white px-1.5 py-0.5 rounded font-mono text-cyan-700">
                  0.7 × vector + 0.3 × keyword
                </code>
              </p>
            </div>
          </div>
          </>)}

          {/* Matched Keywords (if available) */}
          {result.matchedTerms && result.matchedTerms.length > 0 && (
            <div>
              <h3 className="text-sm font-semibold text-muted-foreground uppercase tracking-wide mb-2">
                Matched Keywords (BM25)
              </h3>
              <div className="flex flex-wrap gap-1.5">
                {result.matchedTerms.map((term, i) => (
                  <span
                    key={i}
                    className="px-2 py-1 bg-amber-100 text-amber-700 rounded text-sm font-medium"
                  >
                    {term}
                  </span>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

export default ResultJourneyModal;
