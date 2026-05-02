import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { MessageSquare, FileText, Hash, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

export interface SourceAttribution {
  id: string;
  sourceType: "rag" | "graph" | "profile" | "memory";
  sourceText: string;
  chunkId?: string;
  documentName?: string;
  entityName?: string;
  score?: number;
}

export interface LLMResponse {
  id: string;
  text: string;
  sources: SourceAttribution[];
  timestamp?: number;
}

interface LLMResponsePanelProps {
  response: LLMResponse | null;
  isLoading?: boolean;
  title?: string;
  onSourceClick?: (source: SourceAttribution) => void;
  className?: string;
}

// Source type configuration with colors
const sourceTypeConfig: Record<
  SourceAttribution["sourceType"],
  {
    label: string;
    color: string;
    bgColor: string;
    borderColor: string;
  }
> = {
  rag: {
    label: "RAG",
    color: "text-amber-700",
    bgColor: "bg-amber-100",
    borderColor: "border-amber-300",
  },
  graph: {
    label: "Graph",
    color: "text-cyan-700",
    bgColor: "bg-cyan-100",
    borderColor: "border-cyan-300",
  },
  profile: {
    label: "Profile",
    color: "text-purple-700",
    bgColor: "bg-purple-100",
    borderColor: "border-purple-300",
  },
  memory: {
    label: "Memory",
    color: "text-yellow-700",
    bgColor: "bg-yellow-100",
    borderColor: "border-yellow-300",
  },
};

// Parse response text and add inline source citations
const parseResponseWithSources = (
  text: string,
  sources: SourceAttribution[]
): React.ReactNode => {
  // Simple approach: look for [1], [2], etc. in the text and colorize them
  const sourcePattern = /\[(\d+)\]/g;
  const parts: React.ReactNode[] = [];
  let lastIndex = 0;
  let match;

  while ((match = sourcePattern.exec(text)) !== null) {
    const sourceIndex = parseInt(match[1], 10) - 1;
    const source = sources[sourceIndex];

    // Add text before the citation
    if (match.index > lastIndex) {
      parts.push(
        <span key={`text-${lastIndex}`}>{text.slice(lastIndex, match.index)}</span>
      );
    }

    // Add colored citation
    if (source) {
      const config = sourceTypeConfig[source.sourceType];
      parts.push(
        <span
          key={`citation-${match.index}`}
          className={cn(
            "inline-flex items-center px-1.5 py-0.5 rounded text-xs font-semibold mx-0.5",
            config.bgColor,
            config.color
          )}
        >
          {match[0]}
        </span>
      );
    } else {
      parts.push(<span key={`citation-${match.index}`}>{match[0]}</span>);
    }

    lastIndex = match.index + match[0].length;
  }

  // Add remaining text
  if (lastIndex < text.length) {
    parts.push(<span key={`text-${lastIndex}`}>{text.slice(lastIndex)}</span>);
  }

  return parts.length > 0 ? parts : text;
};

const LLMResponsePanel = ({
  response,
  isLoading = false,
  title = "LLM Response",
  onSourceClick,
  className,
}: LLMResponsePanelProps) => {
  const [expandedSource, setExpandedSource] = useState<string | null>(null);

  const toggleSource = (sourceId: string) => {
    setExpandedSource((prev) => (prev === sourceId ? null : sourceId));
  };

  return (
    <Card className={cn("h-full", className)}>
      <CardHeader className="pb-4">
        <CardTitle className="flex items-center gap-2">
          <MessageSquare className="w-5 h-5 text-primary" />
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {isLoading ? (
          <div className="flex flex-col items-center justify-center py-12">
            <div className="w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-3" />
            <p className="text-sm text-muted-foreground">Generating response...</p>
          </div>
        ) : !response ? (
          <div className="text-center py-12 text-muted-foreground">
            <MessageSquare className="w-12 h-12 mx-auto mb-3 opacity-30" />
            <p className="text-sm">No response yet</p>
          </div>
        ) : (
          <>
            {/* Response Text with Inline Citations */}
            <Card className="border-l-4 border-l-primary">
              <CardContent className="pt-4">
                <div className="prose prose-sm max-w-none">
                  <p className="text-sm text-foreground leading-relaxed whitespace-pre-wrap">
                    {parseResponseWithSources(response.text, response.sources)}
                  </p>
                </div>

                {/* Timestamp */}
                {response.timestamp && (
                  <div className="mt-3 text-xs text-muted-foreground">
                    Generated at{" "}
                    {new Date(response.timestamp).toLocaleTimeString()}
                  </div>
                )}
              </CardContent>
            </Card>

            {/* Source Citations */}
            {response.sources.length > 0 && (
              <div>
                <h3 className="text-sm font-medium mb-3 flex items-center gap-2">
                  <FileText className="w-4 h-4" />
                  Sources ({response.sources.length})
                </h3>
                <div className="space-y-2">
                  {response.sources.map((source, idx) => {
                    const config = sourceTypeConfig[source.sourceType];
                    const isExpanded = expandedSource === source.id;

                    return (
                      <Card
                        key={source.id}
                        className={cn(
                          "border-l-4 cursor-pointer transition-all",
                          config.borderColor,
                          "hover:shadow-md"
                        )}
                        onClick={() => {
                          toggleSource(source.id);
                          onSourceClick?.(source);
                        }}
                        data-testid={`source-${source.id}`}
                      >
                        <CardContent className="p-3">
                          {/* Header */}
                          <div className="flex items-start justify-between gap-2 mb-2">
                            <div className="flex items-center gap-2">
                              {/* Citation Number */}
                              <div
                                className={cn(
                                  "w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold",
                                  config.bgColor,
                                  config.color
                                )}
                              >
                                {idx + 1}
                              </div>

                              {/* Source Type Badge */}
                              <span
                                className={cn(
                                  "px-2 py-0.5 rounded-full text-xs font-medium",
                                  config.bgColor,
                                  config.color
                                )}
                              >
                                {config.label}
                              </span>
                            </div>

                            {/* Expand Icon */}
                            <ChevronRight
                              className={cn(
                                "w-4 h-4 text-muted-foreground transition-transform",
                                isExpanded && "rotate-90"
                              )}
                            />
                          </div>

                          {/* Metadata */}
                          <div className="flex items-center gap-3 text-xs text-muted-foreground mb-2">
                            {source.documentName && (
                              <div className="flex items-center gap-1">
                                <FileText className="w-3 h-3" />
                                <span className="truncate max-w-[150px]">
                                  {source.documentName}
                                </span>
                              </div>
                            )}
                            {source.chunkId && (
                              <div className="flex items-center gap-1">
                                <Hash className="w-3 h-3" />
                                <span>{source.chunkId}</span>
                              </div>
                            )}
                            {source.entityName && (
                              <div className="flex items-center gap-1">
                                <span className="font-medium">
                                  {source.entityName}
                                </span>
                              </div>
                            )}
                            {source.score !== undefined && (
                              <span className="font-mono">
                                {source.score.toFixed(3)}
                              </span>
                            )}
                          </div>

                          {/* Source Text (collapsed/expanded) */}
                          <p
                            className={cn(
                              "text-xs text-foreground transition-all",
                              isExpanded ? "line-clamp-none" : "line-clamp-2"
                            )}
                          >
                            {source.sourceText}
                          </p>
                        </CardContent>
                      </Card>
                    );
                  })}
                </div>
              </div>
            )}

            {/* Explanation */}
            <div className="p-3 bg-muted/50 rounded-lg text-xs">
              <p className="text-muted-foreground">
                <strong>Source Attribution:</strong> Citations like{" "}
                <span className="inline-flex items-center px-1.5 py-0.5 rounded bg-amber-100 text-amber-700 font-semibold mx-0.5">
                  [1]
                </span>{" "}
                in the response link to specific chunks, entities, or context
                sources. Colors indicate the source type (RAG, Graph, Profile, or
                Memory).
              </p>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
};

export default LLMResponsePanel;
