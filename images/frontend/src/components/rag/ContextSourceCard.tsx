import { useState, useEffect } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Code, User, FileText, Share2, Database, LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

export type ContextSourceType = "system" | "profile" | "memory" | "rag" | "graph";

export interface ContextSource {
  id: string;
  type: ContextSourceType;
  name: string;
  description: string;
  tokens: number;
  content: string;
  isActive?: boolean;
}

interface ContextSourceCardProps {
  source: ContextSource;
  isSelected?: boolean;
  isAnimating?: boolean;
  animationDelay?: number;
  onClick?: (source: ContextSource) => void;
  className?: string;
}

// Type-specific configuration
const typeConfig: Record<
  ContextSourceType,
  {
    icon: LucideIcon;
    color: string;
    bgColor: string;
    gradientFrom: string;
    gradientTo: string;
  }
> = {
  system: {
    icon: Code,
    color: "text-blue-600",
    bgColor: "bg-blue-500",
    gradientFrom: "from-blue-500/20",
    gradientTo: "to-blue-600/20",
  },
  profile: {
    icon: User,
    color: "text-purple-600",
    bgColor: "bg-purple-500",
    gradientFrom: "from-purple-500/20",
    gradientTo: "to-purple-600/20",
  },
  memory: {
    icon: Database,
    color: "text-pink-600",
    bgColor: "bg-pink-500",
    gradientFrom: "from-pink-500/20",
    gradientTo: "to-pink-600/20",
  },
  rag: {
    icon: FileText,
    color: "text-amber-600",
    bgColor: "bg-amber-500",
    gradientFrom: "from-amber-500/20",
    gradientTo: "to-amber-600/20",
  },
  graph: {
    icon: Share2,
    color: "text-cyan-600",
    bgColor: "bg-cyan-500",
    gradientFrom: "from-cyan-500/20",
    gradientTo: "to-cyan-600/20",
  },
};

const ContextSourceCard = ({
  source,
  isSelected = false,
  isAnimating = false,
  animationDelay = 0,
  onClick,
  className,
}: ContextSourceCardProps) => {
  const [isVisible, setIsVisible] = useState(false);

  useEffect(() => {
    // Delayed appearance animation
    const timer = setTimeout(() => {
      setIsVisible(true);
    }, animationDelay);

    return () => clearTimeout(timer);
  }, [animationDelay]);

  const config = typeConfig[source.type];
  const Icon = config.icon;

  // Calculate token percentage (assume 2000 tokens is 100% for the mini bar)
  const tokenPercent = Math.min((source.tokens / 2000) * 100, 100);

  return (
    <Card
      className={cn(
        "cursor-pointer transition-all duration-300 relative overflow-hidden",
        "hover:shadow-md",
        isSelected
          ? "ring-2 ring-primary shadow-md"
          : "opacity-70 hover:opacity-90",
        isAnimating && "ring-4 ring-cyan-400 animate-pulse",
        isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4",
        className
      )}
      style={{
        transitionDelay: `${animationDelay}ms`,
      }}
      onClick={() => onClick?.(source)}
      data-testid={`context-source-${source.id}`}
    >
      {/* Animated flow effect when animating */}
      {isAnimating && (
        <div
          className={cn(
            "absolute inset-0 bg-gradient-to-r from-transparent via-cyan-400/20 to-transparent animate-shimmer"
          )}
        />
      )}

      {/* Background gradient */}
      {isSelected && (
        <div
          className={cn(
            "absolute inset-0 bg-gradient-to-br opacity-10",
            config.gradientFrom,
            config.gradientTo
          )}
        />
      )}

      <CardContent className="p-4 relative z-10">
        <div className="flex items-start gap-3">
          {/* Icon */}
          <div
            className={cn(
              "p-2.5 rounded-lg shrink-0 transition-colors",
              isSelected ? config.bgColor : "bg-gray-100"
            )}
          >
            <Icon
              className={cn(
                "w-4 h-4 transition-colors",
                isSelected ? "text-white" : config.color
              )}
            />
          </div>

          {/* Content */}
          <div className="flex-1 min-w-0">
            {/* Header */}
            <div className="flex items-start justify-between gap-2 mb-1">
              <h4 className="font-medium text-sm">{source.name}</h4>
              {source.isActive && (
                <div className="w-2 h-2 rounded-full bg-green-500 shrink-0 mt-1" />
              )}
            </div>

            {/* Description */}
            <p className="text-xs text-muted-foreground mb-2 line-clamp-1">
              {source.description}
            </p>

            {/* Token Count */}
            <div className="flex items-center gap-2">
              <span
                className={cn("text-xs font-mono font-semibold", config.color)}
              >
                {source.tokens.toLocaleString()} tokens
              </span>

              {/* Mini Progress Bar */}
              {isSelected && (
                <div className="flex-1 h-1 bg-gray-100 rounded-full overflow-hidden">
                  <div
                    className={cn(
                      "h-full transition-all duration-500 ease-out",
                      config.bgColor
                    )}
                    style={{ width: `${tokenPercent}%` }}
                  />
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Content Preview (only when selected) */}
        {isSelected && (
          <div
            className={cn(
              "mt-3 p-2.5 rounded text-xs text-muted-foreground line-clamp-2",
              "bg-muted/50 transition-all duration-300"
            )}
          >
            {source.content}
          </div>
        )}
      </CardContent>
    </Card>
  );
};

export default ContextSourceCard;
