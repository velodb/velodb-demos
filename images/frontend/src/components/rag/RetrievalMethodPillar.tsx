import { useState } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { FileText, Sparkles, Share2, ChevronDown, ChevronUp, HelpCircle } from "lucide-react";
import { cn } from "@/lib/utils";

export type RetrievalMethod = "keyword" | "semantic" | "graph";

interface MethodConfig {
  id: RetrievalMethod;
  name: string;
  subtitle: string;
  icon: React.ReactNode;
  color: string;
  bgColor: string;
  borderColor: string;
  description: string;
  example: {
    query: string;
    explanation: string;
  };
  sql: string;
  bestFor: string;
}

const methodConfigs: Record<RetrievalMethod, MethodConfig> = {
  keyword: {
    id: "keyword",
    name: "Keyword",
    subtitle: "BM25",
    icon: <FileText className="w-5 h-5" />,
    color: "text-amber-600",
    bgColor: "bg-amber-50",
    borderColor: "border-amber-400",
    description: "Finds documents containing your exact search terms",
    example: {
      query: '"inverted index"',
      explanation: "Finds docs literally mentioning 'inverted index'",
    },
    sql: `INDEX idx_content (content) USING INVERTED
  PROPERTIES("parser"="english")

SELECT * FROM rag_unified
WHERE content MATCH 'inverted index'`,
    bestFor: "Exact terminology, SQL keywords, technical terms",
  },
  semantic: {
    id: "semantic",
    name: "Semantic",
    subtitle: "Vector",
    icon: <Sparkles className="w-5 h-5" />,
    color: "text-purple-600",
    bgColor: "bg-purple-50",
    borderColor: "border-purple-400",
    description: "Finds documents with similar meaning, even without exact words",
    example: {
      query: '"fast text lookup"',
      explanation: "Finds docs about 'inverted index' (same concept!)",
    },
    sql: `INDEX idx_embedding (content_embedding) USING ANN
  PROPERTIES("index_type"="ivf", "dim"="1024")

SELECT * FROM rag_unified
ORDER BY l2_distance(content_embedding, ?)
LIMIT 10`,
    bestFor: "Conceptual queries, intent understanding, synonyms",
  },
  graph: {
    id: "graph",
    name: "Knowledge Graph",
    subtitle: "Entity + MV",
    icon: <Share2 className="w-5 h-5" />,
    color: "text-cyan-600",
    bgColor: "bg-cyan-50",
    borderColor: "border-cyan-400",
    description: "Finds related entities via Materialized View joins (auto-refreshing)",
    example: {
      query: '"VeloDB"',
      explanation: "1-hop: VeloDB → chunks\n2-hop: VeloDB → Apache Doris → related context",
    },
    sql: `-- Materialized View (auto-refreshes ON COMMIT)
CREATE MATERIALIZED VIEW mv_entity_chunks AS
SELECT entity_name, COLLECT_SET(row_id) as chunk_ids
FROM rag_unified
LATERAL VIEW EXPLODE(entity_names) t AS entity_name
GROUP BY entity_name;

-- 2-hop traversal via MV join
WITH hop1 AS (SELECT chunk_ids FROM mv_entity_chunks
              WHERE entity_name = 'VeloDB')
SELECT * FROM rag_unified u
WHERE EXISTS (SELECT 1 FROM mv_entity_chunks m
              WHERE ARRAY_INTERSECT(m.chunk_ids, hop1.chunk_ids) > 0)`,
    bestFor: "Relationship queries, multi-hop reasoning, entity context",
  },
};

interface RetrievalMethodPillarProps {
  method: RetrievalMethod;
  isExpanded?: boolean;
  onToggle?: () => void;
  showTechnical?: boolean;
  isActive?: boolean;
  timing?: number | null;
  className?: string;
}

const RetrievalMethodPillar = ({
  method,
  isExpanded = false,
  onToggle,
  showTechnical = false,
  isActive = false,
  timing = null,
  className,
}: RetrievalMethodPillarProps) => {
  const config = methodConfigs[method];

  return (
    <Card
      className={cn(
        "transition-all duration-300 cursor-pointer overflow-hidden",
        "hover:shadow-lg",
        isExpanded && "ring-2",
        isExpanded && config.borderColor.replace("border-", "ring-"),
        isActive && "animate-pulse ring-2",
        isActive && config.borderColor.replace("border-", "ring-"),
        className
      )}
      onClick={onToggle}
    >
      <CardContent className="p-4">
        {/* Header */}
        <div className="flex items-start justify-between gap-2">
          <div className="flex items-center gap-3">
            {/* Icon */}
            <div
              className={cn(
                "p-2.5 rounded-lg transition-colors",
                isExpanded || isActive ? config.bgColor : "bg-gray-100",
                config.color
              )}
            >
              {config.icon}
            </div>

            {/* Title */}
            <div>
              <h3 className={cn("font-semibold text-base", config.color)}>
                {config.name}
              </h3>
              <p className="text-xs text-muted-foreground">{config.subtitle}</p>
            </div>
          </div>

          {/* Timing + Expand */}
          <div className="flex items-center gap-2">
            {timing !== null && (
              <span className="text-xs font-mono text-green-600 bg-green-50 px-2 py-1 rounded">
                {timing}ms
              </span>
            )}
            {isExpanded ? (
              <ChevronUp className="w-4 h-4 text-muted-foreground" />
            ) : (
              <ChevronDown className="w-4 h-4 text-muted-foreground" />
            )}
          </div>
        </div>

        {/* Description (always visible) */}
        <p className="mt-3 text-sm text-muted-foreground">{config.description}</p>

        {/* Expanded Content */}
        {isExpanded && (
          <div className="mt-4 space-y-4 animate-in slide-in-from-top-2 duration-200">
            {/* Example */}
            <div className={cn("p-3 rounded-lg", config.bgColor)}>
              <div className="text-xs font-medium text-muted-foreground mb-2">
                Example
              </div>
              <div className="space-y-1">
                <div className="font-mono text-sm">
                  Query: {config.example.query}
                </div>
                <div className="text-sm text-muted-foreground whitespace-pre-line">
                  {config.example.explanation}
                </div>
              </div>
            </div>

            {/* SQL (only in technical mode) */}
            {showTechnical && (
              <div className="rounded-lg bg-slate-900 p-3 overflow-x-auto">
                <div className="text-xs font-medium text-slate-400 mb-2">
                  VeloDB SQL
                </div>
                <pre className="text-xs text-slate-300 font-mono whitespace-pre-wrap">
                  {config.sql}
                </pre>
              </div>
            )}

            {/* Best For */}
            <div className="flex items-center gap-2 text-xs">
              <span className="font-medium text-muted-foreground">Best for:</span>
              <span
                className={cn(
                  "px-2 py-1 rounded-full",
                  config.bgColor,
                  config.color
                )}
              >
                {config.bestFor}
              </span>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
};

export default RetrievalMethodPillar;
export { methodConfigs };
