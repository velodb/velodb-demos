import { useMemo } from "react";
import { ArrowUp, ArrowDown, Minus, Search, Sparkles, FileText, Zap, Lightbulb } from "lucide-react";
import { cn } from "@/lib/utils";

// Types for the root cause analysis
export interface RankComparison {
  docId: string;
  documentName: string;
  chunkIndex: number;
  vectorRank: number | null;
  vectorScore: number | null;
  bm25Rank: number | null;
  bm25Score: number | null;
  hybridRank: number | null;
  hybridScore: number | null;
  matchedTerms?: string[];
}

interface RootCauseAnalysisProps {
  query: string;
  comparisons: RankComparison[];
  className?: string;
}

// Calculate why ranking differs
const getWhyDifferent = (item: RankComparison): { reason: string; type: "semantic" | "keyword" | "balanced" | "unknown" } => {
  const vRank = item.vectorRank ?? 999;
  const bRank = item.bm25Rank ?? 999;
  const hRank = item.hybridRank ?? 999;

  // Strong vector, weak BM25 = semantic match
  if (vRank <= 2 && bRank > 3) {
    return { reason: "Semantic match (no keywords)", type: "semantic" };
  }

  // Strong BM25, weak vector = keyword match
  if (bRank <= 2 && vRank > 3) {
    return { reason: "Keyword match only", type: "keyword" };
  }

  // Both good, hybrid best = balanced
  if (vRank <= 3 && bRank <= 3 && hRank === 1) {
    return { reason: "Strong in both signals", type: "balanced" };
  }

  // Hybrid promoted it (better than both individual)
  if (hRank < vRank && hRank < bRank) {
    return { reason: "RRF boosted (appears in both)", type: "balanced" };
  }

  // Hybrid rank between vector and BM25
  if (hRank >= Math.min(vRank, bRank) && hRank <= Math.max(vRank, bRank)) {
    return { reason: "Averaged by RRF fusion", type: "balanced" };
  }

  // Vector strong
  if (vRank < bRank) {
    return { reason: "Semantic similarity wins", type: "semantic" };
  }

  // BM25 strong
  if (bRank < vRank) {
    return { reason: "Exact keywords match", type: "keyword" };
  }

  return { reason: "Similar across methods", type: "unknown" };
};

// Calculate movement from vector baseline
const getMovement = (vectorRank: number | null, hybridRank: number | null): { delta: number; direction: "up" | "down" | "same" } => {
  if (vectorRank === null || hybridRank === null) {
    return { delta: 0, direction: "same" };
  }

  const delta = vectorRank - hybridRank;

  if (delta > 0) {
    return { delta, direction: "up" }; // Improved (lower rank is better)
  } else if (delta < 0) {
    return { delta: Math.abs(delta), direction: "down" }; // Dropped
  }

  return { delta: 0, direction: "same" };
};

// Movement indicator component
const MovementIndicator = ({ vectorRank, hybridRank }: { vectorRank: number | null; hybridRank: number | null }) => {
  const { delta, direction } = getMovement(vectorRank, hybridRank);

  if (direction === "same") {
    return (
      <span className="flex items-center gap-1 text-gray-500 text-xs">
        <Minus className="w-3 h-3" />
        <span>=</span>
      </span>
    );
  }

  if (direction === "up") {
    return (
      <span className="flex items-center gap-1 text-green-600 text-xs font-medium">
        <ArrowUp className="w-3 h-3" />
        <span>+{delta}</span>
      </span>
    );
  }

  return (
    <span className="flex items-center gap-1 text-red-600 text-xs font-medium">
      <ArrowDown className="w-3 h-3" />
      <span>-{delta}</span>
    </span>
  );
};

// Rank badge component
const RankBadge = ({ rank, color }: { rank: number | null; color: string }) => {
  if (rank === null) {
    return <span className="text-gray-400 text-xs">—</span>;
  }

  return (
    <span className={cn("font-mono text-xs font-semibold", color)}>
      #{rank}
    </span>
  );
};

// Why Different badge
const WhyBadge = ({ item }: { item: RankComparison }) => {
  const { reason, type } = getWhyDifferent(item);

  const colors = {
    semantic: "bg-purple-100 text-purple-700 border-purple-200",
    keyword: "bg-amber-100 text-amber-700 border-amber-200",
    balanced: "bg-cyan-100 text-cyan-700 border-cyan-200",
    unknown: "bg-gray-100 text-gray-600 border-gray-200",
  };

  return (
    <span className={cn("text-xs px-2 py-0.5 rounded-full border", colors[type])}>
      {reason}
    </span>
  );
};

const RootCauseAnalysis = ({ query, comparisons, className }: RootCauseAnalysisProps) => {
  // Generate insight based on the data
  const insight = useMemo(() => {
    if (comparisons.length === 0) return null;

    // Count how many improved in hybrid
    const improved = comparisons.filter((c) => {
      const { direction } = getMovement(c.vectorRank, c.hybridRank);
      return direction === "up";
    }).length;

    const dropped = comparisons.filter((c) => {
      const { direction } = getMovement(c.vectorRank, c.hybridRank);
      return direction === "down";
    }).length;

    // Find best hybrid result that wasn't #1 in either
    const hybridWin = comparisons.find(
      (c) => c.hybridRank === 1 && (c.vectorRank !== 1 && c.bm25Rank !== 1)
    );

    if (hybridWin) {
      return `Hybrid search promoted "${hybridWin.documentName}" to #1 by combining signals from Vector (#${hybridWin.vectorRank}) and BM25 (#${hybridWin.bm25Rank}).`;
    }

    if (improved > dropped) {
      return `Hybrid improves ranking for ${improved} of ${comparisons.length} results by combining semantic and keyword signals.`;
    }

    return "Hybrid search balances Vector's semantic understanding with BM25's keyword precision using RRF fusion.";
  }, [comparisons]);

  if (comparisons.length === 0) {
    return null;
  }

  return (
    <div className={cn("border rounded-lg overflow-hidden", className)} data-testid="root-cause-analysis">
      {/* Header */}
      <div className="bg-slate-50 px-4 py-3 border-b">
        <div className="flex items-center gap-2">
          <Search className="w-4 h-4 text-slate-600" />
          <h3 className="font-semibold text-sm text-slate-700">ROOT CAUSE ANALYSIS</h3>
        </div>
        {query && (
          <p className="text-xs text-muted-foreground mt-1 truncate">
            Query: "{query}"
          </p>
        )}
      </div>

      {/* Table */}
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="bg-gray-50 border-b">
              <th className="text-left px-4 py-2 font-medium text-gray-600">Document</th>
              <th className="text-center px-3 py-2 font-medium">
                <span className="flex items-center justify-center gap-1 text-purple-600">
                  <Sparkles className="w-3 h-3" />
                  Vector
                </span>
              </th>
              <th className="text-center px-3 py-2 font-medium">
                <span className="flex items-center justify-center gap-1 text-amber-600">
                  <FileText className="w-3 h-3" />
                  BM25
                </span>
              </th>
              <th className="text-center px-3 py-2 font-medium">
                <span className="flex items-center justify-center gap-1 text-cyan-600">
                  <Zap className="w-3 h-3" />
                  Hybrid
                </span>
              </th>
              <th className="text-center px-3 py-2 font-medium text-gray-600">Δ</th>
              <th className="text-left px-4 py-2 font-medium text-gray-600">Why Different</th>
            </tr>
          </thead>
          <tbody>
            {comparisons.map((item, index) => (
              <tr
                key={item.docId}
                className={cn(
                  "border-b last:border-b-0 hover:bg-gray-50/50 transition-colors",
                  index % 2 === 0 ? "bg-white" : "bg-gray-50/30"
                )}
                data-testid={`rca-row-${item.docId}`}
              >
                <td className="px-4 py-2.5">
                  <div className="flex flex-col">
                    <span className="font-medium text-gray-900 truncate max-w-[180px]" title={item.documentName}>
                      {item.documentName}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      Chunk {item.chunkIndex}
                    </span>
                  </div>
                </td>
                <td className="text-center px-3 py-2.5">
                  <RankBadge rank={item.vectorRank} color="text-purple-600" />
                </td>
                <td className="text-center px-3 py-2.5">
                  <RankBadge rank={item.bm25Rank} color="text-amber-600" />
                </td>
                <td className="text-center px-3 py-2.5">
                  <RankBadge rank={item.hybridRank} color="text-cyan-600" />
                </td>
                <td className="text-center px-3 py-2.5">
                  <MovementIndicator vectorRank={item.vectorRank} hybridRank={item.hybridRank} />
                </td>
                <td className="px-4 py-2.5">
                  <WhyBadge item={item} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Insight */}
      {insight && (
        <div className="px-4 py-3 bg-cyan-50 border-t border-cyan-100">
          <div className="flex items-start gap-2">
            <Lightbulb className="w-4 h-4 text-cyan-600 mt-0.5 flex-shrink-0" />
            <p className="text-xs text-cyan-800">
              <span className="font-medium">INSIGHT:</span> {insight}
            </p>
          </div>
        </div>
      )}
    </div>
  );
};

export default RootCauseAnalysis;
