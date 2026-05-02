import { Handle, Position, NodeProps } from "@xyflow/react";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Image, Table2, Code2, Calculator } from "lucide-react";

export interface MultimodalIndicators {
  hasImage?: boolean;
  hasTable?: boolean;
  hasCode?: boolean;
  hasFormula?: boolean;
}

export interface EntityNodeData {
  entityId: string;
  entityName: string;
  entityType: string;
  relationshipCount?: number;
  multimodal?: MultimodalIndicators;
  isHub?: boolean;
  onClick?: () => void;
}

// Color mapping for VeloDB entity types
const ENTITY_TYPE_COLORS: Record<string, { bg: string; border: string; text: string }> = {
  // VeloDB-specific types
  model: {
    bg: "bg-purple-100",
    border: "border-purple-500",
    text: "text-purple-700",
  },
  method: {
    bg: "bg-blue-100",
    border: "border-blue-500",
    text: "text-blue-700",
  },
  concept: {
    bg: "bg-orange-100",
    border: "border-orange-500",
    text: "text-orange-700",
  },
  component: {
    bg: "bg-green-100",
    border: "border-green-500",
    text: "text-green-700",
  },
  filter: {
    bg: "bg-cyan-100",
    border: "border-cyan-500",
    text: "text-cyan-700",
  },
  table: {
    bg: "bg-pink-100",
    border: "border-pink-500",
    text: "text-pink-700",
  },
  column: {
    bg: "bg-rose-100",
    border: "border-rose-400",
    text: "text-rose-600",
  },
  parameter: {
    bg: "bg-slate-100",
    border: "border-slate-400",
    text: "text-slate-600",
  },
  function: {
    bg: "bg-indigo-100",
    border: "border-indigo-500",
    text: "text-indigo-700",
  },
  metric: {
    bg: "bg-amber-100",
    border: "border-amber-500",
    text: "text-amber-700",
  },
  dataset: {
    bg: "bg-emerald-100",
    border: "border-emerald-500",
    text: "text-emerald-700",
  },
  // Fallback types
  person: {
    bg: "bg-blue-100",
    border: "border-blue-400",
    text: "text-blue-700",
  },
  organization: {
    bg: "bg-purple-100",
    border: "border-purple-400",
    text: "text-purple-700",
  },
  default: {
    bg: "bg-gray-100",
    border: "border-gray-400",
    text: "text-gray-700",
  },
};

// Multimodal content type indicators
const MULTIMODAL_ICONS = [
  { key: 'hasImage', icon: Image, color: 'bg-green-500', title: 'Has diagrams' },
  { key: 'hasTable', icon: Table2, color: 'bg-amber-500', title: 'Has tables' },
  { key: 'hasCode', icon: Code2, color: 'bg-blue-500', title: 'Has code' },
  { key: 'hasFormula', icon: Calculator, color: 'bg-purple-500', title: 'Has formulas' },
] as const;

const EntityNode = ({ data }: NodeProps<EntityNodeData>) => {
  const { entityName, entityType, relationshipCount = 0, multimodal, isHub } = data;
  const normalizedType = entityType.toLowerCase();
  const colors = ENTITY_TYPE_COLORS[normalizedType] || ENTITY_TYPE_COLORS.default;

  // Get active multimodal indicators
  const activeIndicators = MULTIMODAL_ICONS.filter(
    (ind) => multimodal?.[ind.key as keyof MultimodalIndicators]
  );

  return (
    <div
      className={cn(
        "rounded-full border-2 shadow-md relative cursor-pointer hover:shadow-lg transition-all hover:scale-105",
        colors.bg,
        colors.border,
        isHub && "ring-2 ring-offset-2 ring-purple-400 shadow-lg"
      )}
      style={{
        minWidth: isHub ? 140 : 120,
        maxWidth: isHub ? 200 : 180,
        transform: isHub ? "scale(1.1)" : undefined
      }}
      onClick={data.onClick}
      data-testid="entity-node"
      data-entity-type={entityType}
    >
      {/* Multimodal content indicators - positioned in a row at top right */}
      {activeIndicators.length > 0 && (
        <div className="absolute -top-1 -right-1 flex gap-0.5">
          {activeIndicators.map((ind, idx) => (
            <div
              key={ind.key}
              className={cn(
                "w-4 h-4 rounded-full flex items-center justify-center shadow-sm",
                ind.color
              )}
              title={ind.title}
              style={{ marginLeft: idx > 0 ? -4 : 0 }}
            >
              <ind.icon className="w-2.5 h-2.5 text-white" />
            </div>
          ))}
        </div>
      )}

      {/* Handles for connections */}
      <Handle type="target" position={Position.Top} className={cn("!w-2 !h-2", `!${colors.border.replace("border-", "bg-")}`)} />
      <Handle type="source" position={Position.Bottom} className={cn("!w-2 !h-2", `!${colors.border.replace("border-", "bg-")}`)} />
      <Handle type="target" position={Position.Left} className={cn("!w-2 !h-2", `!${colors.border.replace("border-", "bg-")}`)} />
      <Handle type="source" position={Position.Right} className={cn("!w-2 !h-2", `!${colors.border.replace("border-", "bg-")}`)} />

      {/* Content */}
      <div className="px-3 py-2">
        {/* Entity name */}
        <div className={cn("font-semibold text-xs text-center truncate", colors.text, isHub && "text-sm")} title={entityName}>
          {entityName}
        </div>

        {/* Entity type and relationship count */}
        <div className="flex items-center justify-center gap-1.5 mt-1">
          <Badge variant="secondary" className={cn("h-4 px-1.5 text-[9px]", colors.bg, colors.text, colors.border)}>
            {entityType}
          </Badge>
          {relationshipCount > 0 && (
            <span className="text-[9px] text-gray-500" title={`${relationshipCount} relationships`}>
              ({relationshipCount})
            </span>
          )}
        </div>
      </div>
    </div>
  );
};

export default EntityNode;
