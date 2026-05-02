import { useState, useEffect, useCallback } from "react";
import { Search, Sparkles, FileText, Zap, BarChart3, GitMerge, ArrowRight, Check } from "lucide-react";
import { cn } from "@/lib/utils";

export type SearchMethod = "vector" | "bm25" | "hybrid" | "compare";

export type AnimationStage =
  | "idle"
  | "query"
  | "split"
  | "processing"
  | "fusing"
  | "results"
  | "complete";

interface SearchPipelineAnimationProps {
  method: SearchMethod;
  isAnimating: boolean;
  query?: string;
  onStageChange?: (stage: AnimationStage) => void;
  onComplete?: () => void;
  className?: string;
}

// Stage configurations per method
const METHOD_STAGES: Record<SearchMethod, { name: string; label: string; duration: number }[]> = {
  vector: [
    { name: "query", label: "Query", duration: 300 },
    { name: "processing", label: "Embed 1024d", duration: 600 },
    { name: "processing", label: "Vector Space", duration: 400 },
    { name: "results", label: "Results", duration: 200 },
  ],
  bm25: [
    { name: "query", label: "Query", duration: 300 },
    { name: "processing", label: "Tokenize", duration: 500 },
    { name: "processing", label: "Inverted Index", duration: 400 },
    { name: "results", label: "Results", duration: 200 },
  ],
  hybrid: [
    { name: "query", label: "Query", duration: 300 },
    { name: "split", label: "Split", duration: 300 },
    { name: "processing", label: "Vector + BM25", duration: 600 },
    { name: "fusing", label: "RRF Fuse", duration: 300 },
    { name: "results", label: "Results", duration: 200 },
  ],
  compare: [
    { name: "query", label: "Query", duration: 300 },
    { name: "split", label: "Split ×3", duration: 300 },
    { name: "processing", label: "All Methods", duration: 700 },
    { name: "results", label: "Compare", duration: 200 },
  ],
};

// Animated dots component for vector embedding visualization
const EmbeddingDots = ({ isActive }: { isActive: boolean }) => (
  <div className="flex items-center justify-center gap-0.5">
    {[...Array(5)].map((_, i) => (
      <div
        key={i}
        className={cn(
          "w-1.5 h-1.5 rounded-full transition-all duration-300",
          isActive
            ? "bg-purple-400 animate-pulse"
            : "bg-purple-400/30",
          isActive && `animation-delay-${i * 100}`
        )}
        style={{ animationDelay: isActive ? `${i * 100}ms` : undefined }}
      />
    ))}
  </div>
);

// Animated keyword tokens for BM25 visualization
const KeywordTokens = ({ isActive }: { isActive: boolean }) => (
  <div className="flex items-center gap-0.5">
    {["▓", "░", "█", "░", "▓"].map((char, i) => (
      <span
        key={i}
        className={cn(
          "text-xs font-mono transition-all duration-200",
          isActive
            ? "text-amber-400"
            : "text-amber-400/30",
        )}
        style={{
          animationDelay: isActive ? `${i * 80}ms` : undefined,
          opacity: isActive ? 1 : 0.3,
          transform: isActive ? "scale(1.1)" : "scale(1)"
        }}
      >
        {char}
      </span>
    ))}
  </div>
);

// Stage box component
const StageBox = ({
  label,
  icon: Icon,
  color,
  isActive,
  isComplete,
  children
}: {
  label: string;
  icon?: React.ElementType;
  color: string;
  isActive: boolean;
  isComplete: boolean;
  children?: React.ReactNode;
}) => (
  <div className="flex flex-col items-center gap-1.5">
    <div
      className={cn(
        "relative w-14 h-10 rounded-lg flex items-center justify-center transition-all duration-300",
        isActive && "ring-2 ring-offset-1 ring-offset-slate-900",
        isComplete ? `${color} bg-opacity-40` : `${color} bg-opacity-20`,
        isActive && `${color.replace('bg-', 'ring-')}`
      )}
    >
      {Icon && <Icon className={cn(
        "w-4 h-4 transition-all duration-300",
        isActive || isComplete ? color.replace('bg-', 'text-') : "text-slate-500"
      )} />}
      {children}
      {isComplete && (
        <div className="absolute -top-1 -right-1 w-3.5 h-3.5 bg-green-500 rounded-full flex items-center justify-center">
          <Check className="w-2 h-2 text-white" />
        </div>
      )}
    </div>
    <span className={cn(
      "text-[10px] font-medium transition-colors duration-200",
      isActive ? "text-white" : "text-slate-400"
    )}>
      {label}
    </span>
  </div>
);

// Animated arrow between stages
const StageArrow = ({ isActive }: { isActive: boolean }) => (
  <div className="relative px-1">
    <ArrowRight className={cn(
      "w-4 h-4 transition-all duration-300",
      isActive ? "text-cyan-400" : "text-slate-600"
    )} />
    {isActive && (
      <div className="absolute inset-0 flex items-center justify-center">
        <div className="w-1 h-1 bg-cyan-400 rounded-full animate-ping" />
      </div>
    )}
  </div>
);

// Vector Search Pipeline
const VectorPipeline = ({ currentStage, completedStages }: { currentStage: number; completedStages: number[] }) => (
  <div className="flex items-center justify-center gap-2">
    <StageBox
      label="Query"
      icon={Search}
      color="bg-slate-500"
      isActive={currentStage === 0}
      isComplete={completedStages.includes(0)}
    />
    <StageArrow isActive={currentStage >= 1} />
    <StageBox
      label="Embed 1024d"
      color="bg-purple-500"
      isActive={currentStage === 1}
      isComplete={completedStages.includes(1)}
    >
      <EmbeddingDots isActive={currentStage === 1} />
    </StageBox>
    <StageArrow isActive={currentStage >= 2} />
    <StageBox
      label="Vector Space"
      icon={Sparkles}
      color="bg-purple-500"
      isActive={currentStage === 2}
      isComplete={completedStages.includes(2)}
    />
    <StageArrow isActive={currentStage >= 3} />
    <StageBox
      label="Results"
      color="bg-green-500"
      isActive={currentStage === 3}
      isComplete={completedStages.includes(3)}
    >
      <span className="text-[10px] text-green-400 font-mono">████</span>
    </StageBox>
  </div>
);

// BM25 Search Pipeline
const BM25Pipeline = ({ currentStage, completedStages }: { currentStage: number; completedStages: number[] }) => (
  <div className="flex items-center justify-center gap-2">
    <StageBox
      label="Query"
      icon={Search}
      color="bg-slate-500"
      isActive={currentStage === 0}
      isComplete={completedStages.includes(0)}
    />
    <StageArrow isActive={currentStage >= 1} />
    <StageBox
      label="Tokenize"
      color="bg-amber-500"
      isActive={currentStage === 1}
      isComplete={completedStages.includes(1)}
    >
      <KeywordTokens isActive={currentStage === 1} />
    </StageBox>
    <StageArrow isActive={currentStage >= 2} />
    <StageBox
      label="Inverted Index"
      icon={FileText}
      color="bg-amber-500"
      isActive={currentStage === 2}
      isComplete={completedStages.includes(2)}
    />
    <StageArrow isActive={currentStage >= 3} />
    <StageBox
      label="Results"
      color="bg-green-500"
      isActive={currentStage === 3}
      isComplete={completedStages.includes(3)}
    >
      <span className="text-[10px] text-green-400 font-mono">████</span>
    </StageBox>
  </div>
);

// Hybrid Search Pipeline (shows forking paths)
const HybridPipeline = ({ currentStage, completedStages }: { currentStage: number; completedStages: number[] }) => (
  <div className="flex items-center justify-center gap-2">
    <StageBox
      label="Query"
      icon={Search}
      color="bg-slate-500"
      isActive={currentStage === 0}
      isComplete={completedStages.includes(0)}
    />
    <StageArrow isActive={currentStage >= 1} />

    {/* Split stage with forking visual */}
    <div className="flex flex-col items-center gap-1">
      <div className={cn(
        "flex gap-1 transition-all duration-300",
        currentStage >= 1 ? "opacity-100" : "opacity-40"
      )}>
        <div className={cn(
          "w-6 h-5 rounded flex items-center justify-center",
          currentStage === 1 ? "bg-purple-500/40 ring-1 ring-purple-400" : "bg-purple-500/20"
        )}>
          <Sparkles className="w-3 h-3 text-purple-400" />
        </div>
        <div className={cn(
          "w-6 h-5 rounded flex items-center justify-center",
          currentStage === 1 ? "bg-amber-500/40 ring-1 ring-amber-400" : "bg-amber-500/20"
        )}>
          <FileText className="w-3 h-3 text-amber-400" />
        </div>
      </div>
      <span className={cn(
        "text-[10px] font-medium",
        currentStage === 1 ? "text-white" : "text-slate-400"
      )}>Split</span>
    </div>

    <StageArrow isActive={currentStage >= 2} />

    {/* Parallel processing stage */}
    <StageBox
      label="Vector + BM25"
      color="bg-cyan-500"
      isActive={currentStage === 2}
      isComplete={completedStages.includes(2)}
    >
      <div className="flex gap-0.5">
        <EmbeddingDots isActive={currentStage === 2} />
      </div>
    </StageBox>

    <StageArrow isActive={currentStage >= 3} />

    <StageBox
      label="RRF Fuse"
      icon={GitMerge}
      color="bg-cyan-500"
      isActive={currentStage === 3}
      isComplete={completedStages.includes(3)}
    />

    <StageArrow isActive={currentStage >= 4} />

    <StageBox
      label="Results"
      color="bg-green-500"
      isActive={currentStage === 4}
      isComplete={completedStages.includes(4)}
    >
      <span className="text-[10px] text-green-400 font-mono">████</span>
    </StageBox>
  </div>
);

// Compare All Pipeline (shows all 3 methods)
const ComparePipeline = ({ currentStage, completedStages }: { currentStage: number; completedStages: number[] }) => (
  <div className="flex items-center justify-center gap-2">
    <StageBox
      label="Query"
      icon={Search}
      color="bg-slate-500"
      isActive={currentStage === 0}
      isComplete={completedStages.includes(0)}
    />
    <StageArrow isActive={currentStage >= 1} />

    {/* Three-way split */}
    <div className="flex flex-col items-center gap-1">
      <div className={cn(
        "flex gap-0.5 transition-all duration-300",
        currentStage >= 1 ? "opacity-100" : "opacity-40"
      )}>
        <div className={cn(
          "w-5 h-5 rounded flex items-center justify-center",
          currentStage === 1 ? "bg-purple-500/40" : "bg-purple-500/20"
        )}>
          <Sparkles className="w-2.5 h-2.5 text-purple-400" />
        </div>
        <div className={cn(
          "w-5 h-5 rounded flex items-center justify-center",
          currentStage === 1 ? "bg-amber-500/40" : "bg-amber-500/20"
        )}>
          <FileText className="w-2.5 h-2.5 text-amber-400" />
        </div>
        <div className={cn(
          "w-5 h-5 rounded flex items-center justify-center",
          currentStage === 1 ? "bg-cyan-500/40" : "bg-cyan-500/20"
        )}>
          <Zap className="w-2.5 h-2.5 text-cyan-400" />
        </div>
      </div>
      <span className={cn(
        "text-[10px] font-medium",
        currentStage === 1 ? "text-white" : "text-slate-400"
      )}>Split ×3</span>
    </div>

    <StageArrow isActive={currentStage >= 2} />

    {/* All methods processing */}
    <div className="flex flex-col items-center gap-1">
      <div className={cn(
        "w-20 h-8 rounded-lg flex items-center justify-center gap-1 transition-all duration-300",
        currentStage === 2 ? "bg-slate-700 ring-2 ring-cyan-500/50" : "bg-slate-700/50"
      )}>
        <div className={cn("transition-all", currentStage === 2 && "animate-pulse")}>
          <Sparkles className="w-3 h-3 text-purple-400" />
        </div>
        <div className={cn("transition-all", currentStage === 2 && "animate-pulse")} style={{ animationDelay: "100ms" }}>
          <FileText className="w-3 h-3 text-amber-400" />
        </div>
        <div className={cn("transition-all", currentStage === 2 && "animate-pulse")} style={{ animationDelay: "200ms" }}>
          <Zap className="w-3 h-3 text-cyan-400" />
        </div>
      </div>
      <span className={cn(
        "text-[10px] font-medium",
        currentStage === 2 ? "text-white" : "text-slate-400"
      )}>All Methods</span>
    </div>

    <StageArrow isActive={currentStage >= 3} />

    <StageBox
      label="Compare"
      icon={BarChart3}
      color="bg-green-500"
      isActive={currentStage === 3}
      isComplete={completedStages.includes(3)}
    />
  </div>
);

const SearchPipelineAnimation = ({
  method,
  isAnimating,
  onStageChange,
  onComplete,
  className,
}: SearchPipelineAnimationProps) => {
  const [currentStage, setCurrentStage] = useState(-1);
  const [completedStages, setCompletedStages] = useState<number[]>([]);

  const stages = METHOD_STAGES[method];

  // Reset animation state when isAnimating changes to true
  useEffect(() => {
    if (isAnimating) {
      setCurrentStage(0);
      setCompletedStages([]);
    } else {
      // Keep showing completed state when not animating
      if (completedStages.length === stages.length) {
        // Animation was completed, keep the state
      } else {
        setCurrentStage(-1);
        setCompletedStages([]);
      }
    }
  }, [isAnimating, stages.length]);

  // Advance through stages
  useEffect(() => {
    if (!isAnimating || currentStage < 0 || currentStage >= stages.length) return;

    const stage = stages[currentStage];
    onStageChange?.(stage.name as AnimationStage);

    const timer = setTimeout(() => {
      setCompletedStages(prev => [...prev, currentStage]);

      if (currentStage < stages.length - 1) {
        setCurrentStage(prev => prev + 1);
      } else {
        // Animation complete
        onStageChange?.("complete");
        onComplete?.();
      }
    }, stage.duration);

    return () => clearTimeout(timer);
  }, [currentStage, isAnimating, stages, onStageChange, onComplete]);

  // Render the appropriate pipeline
  const renderPipeline = useCallback(() => {
    const props = { currentStage, completedStages };

    switch (method) {
      case "vector":
        return <VectorPipeline {...props} />;
      case "bm25":
        return <BM25Pipeline {...props} />;
      case "hybrid":
        return <HybridPipeline {...props} />;
      case "compare":
        return <ComparePipeline {...props} />;
    }
  }, [method, currentStage, completedStages]);

  return (
    <div
      className={cn(
        "bg-slate-900 rounded-lg p-4 mb-4",
        className
      )}
      data-testid={`search-pipeline-${method}`}
    >
      {renderPipeline()}

      {/* Progress indicator */}
      <div className="flex justify-center gap-1 mt-3">
        {stages.map((_, idx) => (
          <div
            key={idx}
            className={cn(
              "w-2 h-2 rounded-full transition-all duration-300",
              completedStages.includes(idx)
                ? "bg-green-500"
                : currentStage === idx
                ? "bg-cyan-400 animate-pulse"
                : "bg-slate-700"
            )}
          />
        ))}
      </div>
    </div>
  );
};

export default SearchPipelineAnimation;
