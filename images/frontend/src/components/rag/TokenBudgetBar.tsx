import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";

export interface TokenSource {
  name: string;
  tokens: number;
  color: string;
}

interface TokenBudgetBarProps {
  sources: TokenSource[];
  maxTokens?: number;
  className?: string;
}

const TokenBudgetBar = ({ sources, maxTokens = 8192, className }: TokenBudgetBarProps) => {
  const totalTokens = sources.reduce((sum, source) => sum + source.tokens, 0);
  const usagePercent = (totalTokens / maxTokens) * 100;

  return (
    <div className={cn("space-y-3", className)}>
      {/* Token Count Summary */}
      <div className="flex items-center justify-between text-sm">
        <span className="font-medium">Token Budget</span>
        <span className="text-muted-foreground">
          <span className={cn(
            "font-mono font-semibold",
            usagePercent > 90 ? "text-red-600" : usagePercent > 70 ? "text-amber-600" : "text-green-600"
          )}>
            {totalTokens.toLocaleString()}
          </span>
          <span className="text-muted-foreground"> / {maxTokens.toLocaleString()} tokens</span>
        </span>
      </div>

      {/* Stacked Progress Bar */}
      <div className="relative h-6 bg-gray-100 rounded-full overflow-hidden border">
        {sources.reduce((acc, source) => {
          const sourcePercent = (source.tokens / maxTokens) * 100;
          const segment = (
            <div
              key={source.name}
              className={cn("h-full transition-all duration-300", source.color)}
              style={{
                width: `${sourcePercent}%`,
                marginLeft: acc.offset > 0 ? 0 : undefined,
              }}
              title={`${source.name}: ${source.tokens} tokens`}
            />
          );
          acc.segments.push(segment);
          acc.offset += sourcePercent;
          return acc;
        }, { segments: [] as React.ReactNode[], offset: 0 }).segments}
      </div>

      {/* Legend */}
      <div className="flex flex-wrap gap-3 text-xs">
        {sources.map((source) => (
          <div key={source.name} className="flex items-center gap-1.5">
            <div className={cn("w-3 h-3 rounded-sm", source.color)} />
            <span className="text-muted-foreground">
              {source.name}
              <span className="font-mono font-medium ml-1">
                {source.tokens.toLocaleString()}
              </span>
            </span>
          </div>
        ))}
      </div>

      {/* Warning if over budget */}
      {usagePercent > 100 && (
        <div className="text-xs text-red-600 bg-red-50 px-3 py-2 rounded-md">
          Warning: Token budget exceeded by {(totalTokens - maxTokens).toLocaleString()} tokens
        </div>
      )}
    </div>
  );
};

export default TokenBudgetBar;
