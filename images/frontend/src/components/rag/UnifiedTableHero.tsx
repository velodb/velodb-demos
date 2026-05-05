import { Card, CardContent } from "@/components/ui/card";
import { Database, FileText, Sparkles, Share2 } from "lucide-react";
import { cn } from "@/lib/utils";

interface IndexBadgeProps {
  name: string;
  column: string;
  method: string;
  icon: React.ReactNode;
  color: string;
  bgColor: string;
  isActive?: boolean;
}

const IndexBadge = ({
  name,
  column,
  method,
  icon,
  color,
  bgColor,
  isActive = false,
}: IndexBadgeProps) => (
  <div
    className={cn(
      "flex flex-col items-center p-3 rounded-lg transition-all duration-300",
      isActive ? bgColor : "bg-gray-50",
      isActive && "ring-2 ring-offset-1",
      isActive && color.replace("text-", "ring-")
    )}
  >
    <div className={cn("mb-1", color)}>{icon}</div>
    <div className="text-xs font-semibold text-center">{name}</div>
    <div className="text-[10px] text-muted-foreground font-mono">({column})</div>
    <div className={cn("text-[10px] mt-1", color)}>{method}</div>
  </div>
);

interface UnifiedTableHeroProps {
  activeIndex?: "keyword" | "semantic" | "graph" | null;
  showSystemPrompt?: boolean;
  className?: string;
}

const UnifiedTableHero = ({
  activeIndex = null,
  showSystemPrompt = true,
  className,
}: UnifiedTableHeroProps) => {
  return (
    <div className={cn("space-y-3", className)}>
      {/* Main Table Card */}
      <Card
        className={cn(
          "border-2 border-cyan-400 bg-gradient-to-br from-cyan-50 to-white",
          "transition-all duration-300",
          activeIndex && "animate-pulse"
        )}
      >
        <CardContent className="p-4">
          {/* Header */}
          <div className="flex items-center gap-2 mb-4">
            <Database className="w-5 h-5 text-cyan-600" />
            <span className="font-bold text-cyan-700">rag_unified</span>
            <span className="text-xs text-cyan-600 bg-cyan-100 px-2 py-0.5 rounded-full">
              ONE TABLE
            </span>
          </div>

          {/* Three Indexes */}
          <div className="grid grid-cols-3 gap-3">
            <IndexBadge
              name="INVERTED"
              column="content"
              method="Keyword"
              icon={<FileText className="w-4 h-4" />}
              color="text-amber-600"
              bgColor="bg-amber-50"
              isActive={activeIndex === "keyword"}
            />
            <IndexBadge
              name="ANN"
              column="embedding"
              method="Semantic"
              icon={<Sparkles className="w-4 h-4" />}
              color="text-purple-600"
              bgColor="bg-purple-50"
              isActive={activeIndex === "semantic"}
            />
            <IndexBadge
              name="ARRAY INV"
              column="entity_names"
              method="Graph"
              icon={<Share2 className="w-4 h-4" />}
              color="text-cyan-600"
              bgColor="bg-cyan-50"
              isActive={activeIndex === "graph"}
            />
          </div>

          {/* Hero Message */}
          <div className="mt-4 text-center">
            <p className="text-xs text-muted-foreground">
              No Elasticsearch + No Pinecone + No Neo4j
            </p>
            <p className="text-sm font-semibold text-cyan-700 mt-1">
              Just ONE VeloDB table
            </p>
          </div>
        </CardContent>
      </Card>

      {/* System Prompt (optional) */}
      {showSystemPrompt && (
        <Card className="border border-blue-200 bg-blue-50/50">
          <CardContent className="p-3">
            <div className="flex items-center gap-2">
              <div className="p-1.5 rounded bg-blue-100">
                <Database className="w-3.5 h-3.5 text-blue-600" />
              </div>
              <div>
                <span className="text-sm font-medium text-blue-700">
                  System Prompt
                </span>
                <span className="text-xs text-blue-500 ml-2">
                  (stored in VeloDB)
                </span>
              </div>
            </div>
            <p className="text-xs text-muted-foreground mt-2 ml-8">
              Guides LLM behavior, citations, and response formatting
            </p>
          </CardContent>
        </Card>
      )}
    </div>
  );
};

export default UnifiedTableHero;
