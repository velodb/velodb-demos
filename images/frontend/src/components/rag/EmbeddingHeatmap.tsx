import { useState, useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Activity, ZoomIn, ZoomOut, Info } from "lucide-react";
import { cn } from "@/lib/utils";

export interface EmbeddingData {
  vector: number[];
  model?: string;
  dimension?: number;
}

interface EmbeddingHeatmapProps {
  embedding: EmbeddingData;
  title?: string;
  showStats?: boolean;
  maxDisplayDimensions?: number;
  className?: string;
}

// Helper function to map value to color (blue to red gradient)
const getHeatmapColor = (value: number, min: number, max: number): string => {
  // Normalize value to 0-1 range
  const normalized = (value - min) / (max - min);

  // Generate color from blue (low) to white (mid) to red (high)
  if (normalized < 0.5) {
    // Blue to white
    const intensity = Math.round(normalized * 2 * 255);
    return `rgb(${intensity}, ${intensity}, 255)`;
  } else {
    // White to red
    const intensity = Math.round((1 - normalized) * 2 * 255);
    return `rgb(255, ${intensity}, ${intensity})`;
  }
};

const EmbeddingHeatmap = ({
  embedding,
  title = "Embedding Visualization",
  showStats = true,
  maxDisplayDimensions = 512,
  className,
}: EmbeddingHeatmapProps) => {
  const [cellSize, setCellSize] = useState<"sm" | "md" | "lg">("md");
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);

  // Calculate statistics
  const stats = useMemo(() => {
    const values = embedding.vector;
    const sum = values.reduce((a, b) => a + b, 0);
    const mean = sum / values.length;
    const min = Math.min(...values);
    const max = Math.max(...values);
    const variance =
      values.reduce((acc, val) => acc + Math.pow(val - mean, 2), 0) /
      values.length;
    const stdDev = Math.sqrt(variance);

    return { mean, min, max, stdDev, sum };
  }, [embedding.vector]);

  // Limit dimensions for performance
  const displayVector = useMemo(() => {
    return embedding.vector.slice(0, maxDisplayDimensions);
  }, [embedding.vector, maxDisplayDimensions]);

  // Calculate grid layout (try to make it roughly square)
  const gridCols = useMemo(() => {
    const total = displayVector.length;
    return Math.ceil(Math.sqrt(total));
  }, [displayVector.length]);

  // Cell size mapping
  const cellSizeMap = {
    sm: "w-1.5 h-1.5",
    md: "w-2 h-2",
    lg: "w-3 h-3",
  };

  const handleZoomIn = () => {
    if (cellSize === "sm") setCellSize("md");
    else if (cellSize === "md") setCellSize("lg");
  };

  const handleZoomOut = () => {
    if (cellSize === "lg") setCellSize("md");
    else if (cellSize === "md") setCellSize("sm");
  };

  return (
    <Card className={cn("h-full", className)}>
      <CardHeader className="pb-4">
        <div className="flex items-center justify-between">
          <CardTitle className="flex items-center gap-2">
            <Activity className="w-5 h-5 text-purple-600" />
            {title}
          </CardTitle>

          {/* Zoom Controls */}
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="sm"
              onClick={handleZoomOut}
              disabled={cellSize === "sm"}
            >
              <ZoomOut className="w-4 h-4" />
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={handleZoomIn}
              disabled={cellSize === "lg"}
            >
              <ZoomIn className="w-4 h-4" />
            </Button>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          {embedding.model && `Model: ${embedding.model} • `}
          Dimensions: {embedding.vector.length.toLocaleString()}
          {displayVector.length < embedding.vector.length &&
            ` (showing first ${displayVector.length.toLocaleString()})`}
        </p>
      </CardHeader>

      <CardContent className="space-y-4">
        {/* Heatmap Grid */}
        <div className="relative">
          <div
            className="flex flex-wrap gap-0.5 p-4 bg-gray-50 rounded-lg border overflow-auto max-h-[400px]"
            style={{
              gridTemplateColumns: `repeat(${gridCols}, minmax(0, 1fr))`,
            }}
          >
            {displayVector.map((value, idx) => (
              <div
                key={idx}
                className={cn(
                  "transition-all cursor-pointer hover:ring-2 hover:ring-primary hover:z-10 relative",
                  cellSizeMap[cellSize]
                )}
                style={{
                  backgroundColor: getHeatmapColor(value, stats.min, stats.max),
                }}
                onMouseEnter={() => setHoveredIndex(idx)}
                onMouseLeave={() => setHoveredIndex(null)}
                data-testid={`embedding-cell-${idx}`}
                title={`Index ${idx}: ${value.toFixed(6)}`}
              />
            ))}
          </div>

          {/* Hover Tooltip */}
          {hoveredIndex !== null && (
            <div className="absolute top-2 right-2 bg-white border shadow-lg rounded-lg p-2 text-xs z-20">
              <div className="font-mono">
                <div>
                  <span className="text-muted-foreground">Index:</span>{" "}
                  <span className="font-semibold">{hoveredIndex}</span>
                </div>
                <div>
                  <span className="text-muted-foreground">Value:</span>{" "}
                  <span className="font-semibold">
                    {displayVector[hoveredIndex].toFixed(6)}
                  </span>
                </div>
              </div>
            </div>
          )}
        </div>

        {/* Color Legend */}
        <div className="flex items-center gap-2 text-xs">
          <span className="text-muted-foreground">Scale:</span>
          <div className="flex-1 h-4 rounded bg-gradient-to-r from-blue-500 via-white to-red-500 border" />
          <div className="flex gap-4 font-mono text-muted-foreground">
            <span>{stats.min.toFixed(3)}</span>
            <span>0</span>
            <span>{stats.max.toFixed(3)}</span>
          </div>
        </div>

        {/* Statistics */}
        {showStats && (
          <Card className="border-purple-200 bg-purple-50/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm flex items-center gap-2">
                <Info className="w-4 h-4 text-purple-600" />
                Vector Statistics
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-2 md:grid-cols-3 gap-3 text-xs">
                <div>
                  <div className="text-muted-foreground mb-1">Mean</div>
                  <div className="font-mono font-semibold">
                    {stats.mean.toFixed(6)}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground mb-1">Std Dev</div>
                  <div className="font-mono font-semibold">
                    {stats.stdDev.toFixed(6)}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground mb-1">Min</div>
                  <div className="font-mono font-semibold text-blue-600">
                    {stats.min.toFixed(6)}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground mb-1">Max</div>
                  <div className="font-mono font-semibold text-red-600">
                    {stats.max.toFixed(6)}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground mb-1">L2 Norm</div>
                  <div className="font-mono font-semibold">
                    {Math.sqrt(
                      displayVector.reduce((sum, val) => sum + val * val, 0)
                    ).toFixed(6)}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground mb-1">Sparsity</div>
                  <div className="font-mono font-semibold">
                    {(
                      (displayVector.filter((v) => Math.abs(v) < 0.01).length /
                        displayVector.length) *
                      100
                    ).toFixed(1)}
                    %
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>
        )}

        {/* Explanation */}
        <div className="p-3 bg-muted/50 rounded-lg text-xs">
          <p className="text-muted-foreground">
            <strong>Embedding Heatmap:</strong> Each cell represents one dimension
            of the embedding vector. Colors range from blue (negative values)
            through white (near zero) to red (positive values). Hover over cells to
            see exact values.
          </p>
        </div>
      </CardContent>
    </Card>
  );
};

export default EmbeddingHeatmap;
