import { useState, useCallback, useMemo, useEffect, useRef } from "react";
import { Sidebar } from "@/components/Sidebar";
import { Button } from "@/components/ui/button";
import { KafkaPanel } from "@/components/demo/KafkaPanel";
import { QueryTooltip } from "@/components/demo/QueryTooltip";
import { MaterializedViewCard } from "@/components/demo/MaterializedViewCard";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Zap, Database, Activity, BarChart3, MousePointer, DollarSign, Gauge, Clock, ShoppingCart, Plus, RefreshCw, Trash2, Loader2, ArrowRight, CheckCircle, TrendingUp, Package, Users, Eye, Square, AlertCircle, ExternalLink, Info, Copy, Check } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { toast } from "@/components/ui/use-toast";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { AreaChart, Area, ComposedChart, BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip as RechartsTooltip, ResponsiveContainer, Cell, ReferenceLine, LineChart, Line, Legend } from "recharts";
import {
  ReactFlow,
  Node,
  Edge,
  Background,
  useNodesState,
  useEdgesState,
  MarkerType,
  Handle,
  Position,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";

// SQL queries used in analytics dashboard (from backend/internal/db/velodb.go)
const SQL_QUERIES = {
  ecommerceStats: `SELECT
  COUNT(*) as total_orders,
  IFNULL(SUM(taxful_total_price), 0) as total_revenue,
  IFNULL(AVG(taxful_total_price), 0) as avg_order_value
FROM kibana_sample_data_ecommerce
WHERE partner_id = ?
  AND order_date >= DATE_SUB(NOW(), INTERVAL 24 HOUR)`,
  revenueMetrics: `SELECT
  DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00') as hour,
  SUM(taxful_total_price) as revenue,
  COUNT(*) as order_count,
  AVG(taxful_total_price) as avg_order_value,
  (SUM(taxful_total_price) - LAG(SUM(taxful_total_price))
    OVER (ORDER BY DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00')))
    / NULLIF(LAG(SUM(taxful_total_price))
    OVER (ORDER BY DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00')), 0) as growth_rate
FROM kibana_sample_data_ecommerce
WHERE order_date >= DATE_SUB(NOW(), INTERVAL 24 HOUR)
  AND partner_id = ?
GROUP BY DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00')
ORDER BY hour DESC`,
  conversionFunnel: `-- Uses Materialized View for 40% faster queries
-- MV refresh: EVERY 1 MINUTE, covers last 7 days
SELECT
  COALESCE(views, 0) as views,
  COALESCE(carts, 0) as carts,
  COALESCE(purchases, 0) as purchases
FROM mv_conversion_funnel
WHERE partner_id = ?`,
  revenuePerMinute: `SELECT
  DATE_FORMAT(order_date, '%H:%i') as minute,
  COALESCE(SUM(taxful_total_price), 0) as revenue,
  COUNT(*) as orders
FROM kibana_sample_data_ecommerce
WHERE partner_id = ?
  AND order_date >= DATE_SUB(NOW(), INTERVAL 30 MINUTE)
GROUP BY minute
ORDER BY minute ASC`,
  topProducts: `WITH current_week AS (
  SELECT
    get_json_string(products, '$[0].product_name') as product_name,
    get_json_string(products, '$[0].category') as category,
    SUM(CAST(get_json_double(products, '$[0].taxful_price')
      AS DECIMAL(12,2))) as revenue
  FROM kibana_sample_data_ecommerce
  WHERE order_date >= DATE_SUB(NOW(), INTERVAL 7 DAY)
    AND partner_id = ?
  GROUP BY
    get_json_string(products, '$[0].product_name'),
    get_json_string(products, '$[0].category')
),
prev_week AS (
  SELECT
    get_json_string(products, '$[0].product_name') as product_name,
    SUM(CAST(get_json_double(products, '$[0].taxful_price')
      AS DECIMAL(12,2))) as revenue
  FROM kibana_sample_data_ecommerce
  WHERE order_date >= DATE_SUB(NOW(), INTERVAL 14 DAY)
    AND order_date < DATE_SUB(NOW(), INTERVAL 7 DAY)
    AND partner_id = ?
  GROUP BY get_json_string(products, '$[0].product_name')
),
ranked AS (
  SELECT cw.*, DENSE_RANK() OVER (ORDER BY cw.revenue DESC) as current_rank,
    pw.revenue as prev_revenue
  FROM current_week cw
  LEFT JOIN prev_week pw ON cw.product_name = pw.product_name
)
SELECT product_name, category, revenue, current_rank
FROM ranked WHERE current_rank <= ?`,
  liveOrders: `SELECT
  order_id,
  order_date,
  customer_id,
  IFNULL(customer_full_name, '') as customer_full_name,
  IFNULL(taxful_total_price, 0) as taxful_total_price,
  IFNULL(order_status, 'pending') as order_status,
  IFNULL(products, '[]') as products,
  IFNULL(geoip, '{}') as geoip
FROM kibana_sample_data_ecommerce
WHERE partner_id = ?
ORDER BY order_date DESC
LIMIT 10`,
};

// CREATE TABLE SQL for VeloDB info modal
const VELODB_CREATE_TABLE_SQL = `CREATE TABLE kibana_sample_data_ecommerce (
  customer_id         INT NOT NULL,
  order_id            INT NOT NULL,
  order_date          DATETIME NOT NULL,
  customer_full_name  VARCHAR(255),
  customer_email      VARCHAR(255),
  taxful_total_price  DECIMAL(12,2),
  order_status        VARCHAR(50),
  products            JSON,
  geoip               JSON,
  partner_id          INT DEFAULT 44
)
UNIQUE KEY(customer_id, order_id)
DISTRIBUTED BY HASH(customer_id) BUCKETS 8
PROPERTIES (
  "replication_num" = "3",
  "enable_unique_key_merge_on_write" = "true"
);`;

// SQL syntax highlighting colors (same as QueryTooltip)
const SQL_COLORS = {
  keyword: 'text-blue-400 font-bold',
  special: 'text-purple-400 font-bold',
  function: 'text-cyan-400',
  string: 'text-green-400',
  number: 'text-yellow-400',
  comment: 'text-gray-500 italic',
  default: 'text-slate-300',
};

const SQL_KEYWORDS = [
  'SELECT', 'FROM', 'WHERE', 'JOIN', 'CREATE', 'TABLE', 'NOT', 'NULL', 'DEFAULT',
  'INT', 'VARCHAR', 'DATETIME', 'DECIMAL', 'JSON', 'KEY', 'BY', 'PROPERTIES',
];

const SPECIAL_KEYWORDS = [
  'UNIQUE', 'DISTRIBUTED', 'HASH', 'BUCKETS',
];

// Simple SQL highlighter for CREATE TABLE
function highlightCreateTableSQL(sql: string): React.ReactNode[] {
  const result: React.ReactNode[] = [];
  let remaining = sql;
  let keyIndex = 0;

  while (remaining.length > 0) {
    let matched = false;

    // Check for string literals (single quotes)
    if (remaining.startsWith("'") || remaining.startsWith('"')) {
      const quote = remaining[0];
      const endQuote = remaining.indexOf(quote, 1);
      if (endQuote !== -1) {
        const str = remaining.substring(0, endQuote + 1);
        result.push(<span key={keyIndex++} className={SQL_COLORS.string}>{str}</span>);
        remaining = remaining.substring(endQuote + 1);
        matched = true;
        continue;
      }
    }

    // Check for numbers
    const numberMatch = remaining.match(/^(\d+\.?\d*)/);
    if (numberMatch && /[\s,()=]/.test(result.length > 0 ? remaining.slice(-1) : ' ')) {
      result.push(<span key={keyIndex++} className={SQL_COLORS.number}>{numberMatch[1]}</span>);
      remaining = remaining.substring(numberMatch[1].length);
      matched = true;
      continue;
    }

    // Check for special keywords first
    for (const keyword of SPECIAL_KEYWORDS) {
      const regex = new RegExp(`^(${keyword})\\b`, 'i');
      const match = remaining.match(regex);
      if (match) {
        result.push(<span key={keyIndex++} className={SQL_COLORS.special}>{match[1]}</span>);
        remaining = remaining.substring(match[1].length);
        matched = true;
        break;
      }
    }
    if (matched) continue;

    // Check for SQL keywords
    for (const keyword of SQL_KEYWORDS) {
      const regex = new RegExp(`^(${keyword})\\b`, 'i');
      const match = remaining.match(regex);
      if (match) {
        result.push(<span key={keyIndex++} className={SQL_COLORS.keyword}>{match[1]}</span>);
        remaining = remaining.substring(match[1].length);
        matched = true;
        break;
      }
    }
    if (matched) continue;

    // No match - add single character as default
    result.push(<span key={keyIndex++} className={SQL_COLORS.default}>{remaining[0]}</span>);
    remaining = remaining.substring(1);
  }

  return result;
}

type ArchitectureNodeData = {
  label: string;
  icon: React.ReactNode;
  clickable?: boolean;
  externalLink?: boolean;
  color: string;
  onNodeClick?: (nodeId: string) => void;
  isSpikeActive?: boolean;
  tooltip?: string;
  hiddenHandles?: Partial<Record<"left" | "right" | "top" | "bottom", boolean>>;
};

// Custom node component for architecture diagram
const ArchitectureNode = ({ data, id }: { data: ArchitectureNodeData; id: string }) => {
  const handleClick = () => {
    if (data.clickable && data.onNodeClick) {
      data.onNodeClick(id);
    }
  };

  // Determine if this node should pulse during spike (Postgres, Kafka and VeloDB are the key data processing nodes)
  const shouldPulse = data.isSpikeActive && (id === "postgres" || id === "kafka" || id === "velodb");
  const hiddenHandles = data.hiddenHandles ?? {};

  const nodeContent = (
    <div
      onClick={handleClick}
      className={`px-4 py-3 rounded-lg border-2 shadow-md flex items-center gap-2 min-w-[120px] justify-center transition-all duration-200 ${data.clickable
        ? "cursor-pointer hover:shadow-lg hover:scale-105 border-solid"
        : "cursor-default border-dashed opacity-80"
        } ${shouldPulse ? "spike-node-pulse" : ""}`}
      style={{
        backgroundColor: data.color,
        borderColor: data.clickable ? "#333" : "#999",
        pointerEvents: "auto",
      }}
      data-testid={`node-${id}`}
    >
      {!hiddenHandles.left && (
        <Handle type="target" position={Position.Left} className="!bg-gray-400 !w-2 !h-2" />
      )}
      {!hiddenHandles.top && (
        <Handle type="target" position={Position.Top} id="top" className="!bg-gray-400 !w-2 !h-2" />
      )}
      {data.icon}
      <span className="font-semibold text-sm text-gray-800">{data.label}</span>
      {data.clickable && (
        data.externalLink
          ? <ExternalLink className="h-3 w-3 text-gray-600 ml-1" />
          : <MousePointer className="h-3 w-3 text-gray-600 ml-1" />
      )}
      {!hiddenHandles.right && (
        <Handle type="source" position={Position.Right} className="!bg-gray-400 !w-2 !h-2" />
      )}
      {!hiddenHandles.bottom && (
        <Handle type="source" position={Position.Bottom} id="bottom" className="!bg-gray-400 !w-2 !h-2" />
      )}
    </div>
  );

  // Wrap non-interactive nodes with tooltip
  if (!data.clickable && data.tooltip) {
    return (
      <TooltipProvider delayDuration={200}>
        <Tooltip>
          <TooltipTrigger asChild>
            {nodeContent}
          </TooltipTrigger>
          <TooltipContent side="top" className="max-w-[200px] text-center" data-testid={`tooltip-${id}`}>
            <p>{data.tooltip}</p>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    );
  }

  return nodeContent;
};

// Hero node component for VeloDB - the main product highlight
const HeroNode = ({ data, id }: { data: { label: string; subtitle: string; icon: React.ReactNode; clickable?: boolean; onNodeClick?: (nodeId: string) => void; onInfoClick?: () => void; isSpikeActive?: boolean }; id: string }) => {
  const handleClick = () => {
    if (data.clickable && data.onNodeClick) {
      data.onNodeClick(id);
    }
  };

  const handleInfoClick = (e: React.MouseEvent) => {
    e.stopPropagation(); // Prevent node click
    if (data.onInfoClick) {
      data.onInfoClick();
    }
  };

  const shouldPulse = data.isSpikeActive;

  return (
    <div
      onClick={handleClick}
      className={`relative px-6 py-4 rounded-xl border-4 shadow-xl flex flex-col items-center gap-1 min-w-[160px] transition-all duration-200 cursor-pointer hover:shadow-2xl hover:scale-105 ${shouldPulse ? "spike-node-pulse" : ""}`}
      style={{
        background: "linear-gradient(135deg, #F0F9FF 0%, #E0F2FE 50%, #BAE6FD 100%)",
        borderColor: "#0EA5E9",
        boxShadow: "0 4px 14px rgba(14, 165, 233, 0.25), 0 0 0 4px rgba(14, 165, 233, 0.1)",
        pointerEvents: "auto",
      }}
      data-testid={`node-${id}`}
    >
      {/* Info icon in top-right corner */}
      <button
        onClick={handleInfoClick}
        className="absolute top-1 right-1 p-1 rounded-full bg-sky-100 hover:bg-sky-200 transition-colors z-10"
        data-testid="velodb-info-button"
        title="View VeloDB Hash Distribution"
      >
        <Info className="h-3.5 w-3.5 text-sky-600" />
      </button>
      <Handle type="target" position={Position.Left} className="!bg-white !w-3 !h-3 !border-2 !border-sky-500" />
      <Handle type="target" position={Position.Bottom} id="bottom-in" className="!bg-white !w-3 !h-3 !border-2 !border-sky-500" />
      <div className="flex items-center gap-2">
        {data.icon}
        <span className="font-bold text-lg text-sky-700 drop-shadow-sm">{data.label}</span>
        <MousePointer className="h-4 w-4 text-sky-600/80" />
      </div>
      <span className="text-xs text-sky-600 font-medium">{data.subtitle}</span>
      <Handle type="source" position={Position.Right} className="!bg-white !w-3 !h-3 !border-2 !border-sky-500" />
      <Handle type="source" position={Position.Bottom} id="bottom-out" className="!bg-white !w-3 !h-3 !border-2 !border-sky-500" />
    </div>
  );
};

// Group label node for visual grouping
const GroupLabelNode = ({ data }: { data: { label: string; color: string } }) => {
  return (
    <div
      className="px-3 py-1 rounded-md text-xs font-semibold uppercase tracking-wide"
      style={{
        backgroundColor: data.color,
        color: "#666",
        pointerEvents: "none",
      }}
    >
      {data.label}
    </div>
  );
};

const nodeTypes = {
  architecture: ArchitectureNode,
  hero: HeroNode,
  groupLabel: GroupLabelNode,
};

// Animated arrow component showing data flow from left to right
const AnimatedDataFlowArrow = () => (
  <div className="flex items-center gap-0.5 px-2" data-testid="animated-arrow">
    {/* Animated dots flowing left to right */}
    <div className="relative w-8 h-4 flex items-center overflow-hidden">
      <div className="absolute inset-0 flex items-center">
        <div className="animate-flow-dot w-1.5 h-1.5 rounded-full bg-blue-400 opacity-80" style={{ animationDelay: '0ms' }} />
        <div className="animate-flow-dot w-1.5 h-1.5 rounded-full bg-blue-500 opacity-90 ml-2" style={{ animationDelay: '200ms' }} />
        <div className="animate-flow-dot w-1.5 h-1.5 rounded-full bg-blue-600 ml-2" style={{ animationDelay: '400ms' }} />
      </div>
    </div>
    <ArrowRight className="h-4 w-4 text-blue-600" />
  </div>
);

// Initial nodes for the architecture diagram
// Layout: Sources -> Ingestion -> VeloDB (hero) -> Apps
//         Storage layer below (Kafka -> Data Lake <-> Object Storage <- VeloDB)
const initialNodes: Node[] = [
  // === SOURCES (left column) ===
  {
    id: "postgres",
    type: "architecture",
    position: { x: 0, y: 0 },
    data: {
      label: "Postgres",
      icon: <Database className="h-4 w-4 text-blue-800" />,
      clickable: true,
      color: "#DBEAFE",
    },
  },
  {
    id: "clickstream",
    type: "architecture",
    position: { x: 0, y: 80 },
    data: {
      label: "Clickstream",
      icon: <Activity className="h-4 w-4 text-purple-600" />,
      clickable: false,
      color: "#E9D5FF",
      tooltip: "Real-time user event stream",
    },
  },

  // === INGESTION (center-left column) ===
  {
    id: "flink",
    type: "architecture",
    position: { x: 180, y: 0 },
    data: {
      label: "Flink CDC",
      icon: <Activity className="h-4 w-4 text-pink-600" />,
      clickable: false,
      color: "#FBCFE8",
      tooltip: "Change Data Capture",
    },
  },
  {
    id: "kafka",
    type: "architecture",
    position: { x: 180, y: 80 },
    data: {
      label: "Kafka",
      icon: <Activity className="h-4 w-4 text-orange-600" />,
      clickable: true,
      color: "#FED7AA",
    },
  },

  // === VELODB - HERO NODE (center) ===
  {
    id: "velodb",
    type: "hero",
    position: { x: 360, y: 25 },
    data: {
      label: "VeloDB",
      subtitle: "Real-Time Analytics",
      icon: <Database className="h-5 w-5 text-white" />,
      clickable: true,
    },
  },

  // === ANALYTICS APPS (right column) ===
  {
    id: "grafana",
    type: "architecture",
    position: { x: 560, y: 0 },
    data: {
      label: "Grafana",
      icon: <BarChart3 className="h-4 w-4 text-orange-500" />,
      clickable: false,
      color: "#FFEDD5",
      tooltip: "Metrics dashboards",
    },
  },
  // === STORAGE LAYER (bottom row) ===
  {
    id: "datalake",
    type: "architecture",
    position: { x: 220, y: 175 },
    data: {
      label: "Data Lake",
      icon: <Database className="h-4 w-4 text-cyan-600" />,
      clickable: false,
      color: "#CFFAFE",
      tooltip: "Iceberg/Delta Lake",
      hiddenHandles: { left: true, bottom: true },
    },
  },
  {
    id: "objectstorage",
    type: "architecture",
    position: { x: 420, y: 175 },
    data: {
      label: "Object Storage",
      icon: <Database className="h-4 w-4 text-indigo-600" />,
      clickable: false,
      color: "#E0E7FF",
      tooltip: "S3/GCS/Azure Blob",
      hiddenHandles: { right: true },
    },
  },
];

// Base edges configuration
// Main flow: Sources → Ingestion → VeloDB → Apps
// Storage: Kafka → Data Lake ↔ Object Storage ← VeloDB
const baseEdgesConfig = [
  // === MAIN FLOW (animated, prominent) ===
  // Upper path: Postgres → Flink CDC → VeloDB
  {
    id: "postgres-flink",
    source: "postgres",
    target: "flink",
    animated: true,
    style: { stroke: "#2563EB", strokeWidth: 2 },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#2563EB" },
  },
  {
    id: "flink-velodb",
    source: "flink",
    target: "velodb",
    animated: true,
    style: { stroke: "#DB2777", strokeWidth: 2 },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#DB2777" },
  },
  // Lower path: Clickstream → Kafka → VeloDB
  {
    id: "clickstream-kafka",
    source: "clickstream",
    target: "kafka",
    animated: true,
    style: { stroke: "#9333EA", strokeWidth: 2 },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#9333EA" },
  },
  {
    id: "kafka-velodb",
    source: "kafka",
    target: "velodb",
    animated: true,
    style: { stroke: "#EA580C", strokeWidth: 2 },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#EA580C" },
  },
  // Output: VeloDB → Apps
  {
    id: "velodb-grafana",
    source: "velodb",
    target: "grafana",
    animated: true,
    style: { stroke: "#ef4444", strokeWidth: 2 },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#ef4444" },
  },
  // === STORAGE LAYER (dashed, secondary) ===
  // Kafka (bottom) → Data Lake (top)
  {
    id: "kafka-datalake",
    source: "kafka",
    target: "datalake",
    sourceHandle: "bottom",
    targetHandle: "top",
    animated: false,
    style: { stroke: "#06B6D4", strokeWidth: 1.5, strokeDasharray: "4,4" },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#06B6D4" },
  },
  // Data Lake → VeloDB (bottom)
  {
    id: "datalake-velodb",
    source: "datalake",
    target: "velodb",
    targetHandle: "bottom-in",
    animated: false,
    style: { stroke: "#06B6D4", strokeWidth: 1.5, strokeDasharray: "4,4" },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#06B6D4" },
  },
  // VeloDB (bottom) → Object Storage
  {
    id: "velodb-objectstorage",
    source: "velodb",
    target: "objectstorage",
    sourceHandle: "bottom-out",
    targetHandle: "top",
    animated: false,
    style: { stroke: "#6366F1", strokeWidth: 1.5, strokeDasharray: "4,4" },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#6366F1" },
  },
  // Data Lake ↔ Object Storage (bidirectional)
  {
    id: "datalake-objectstorage",
    source: "datalake",
    target: "objectstorage",
    animated: false,
    style: { stroke: "#6366F1", strokeWidth: 1.5 },
    markerEnd: { type: MarkerType.ArrowClosed, color: "#6366F1" },
    markerStart: { type: MarkerType.ArrowClosed, color: "#6366F1" },
  },
];

type PanelType = "welcome" | "cdc" | "dashboard" | "spike" | "kafka";

// CDC workflow state
type CDCState = "idle" | "inserted" | "updated";

interface ReplicationLogEntry {
  id: string;
  operation: "INSERT" | "UPDATE" | "DELETE";
  orderId: number;
  timestamp: Date;
  postgresStatus: "COMMITTED" | "DELETED";
  velodbStatus: "SYNCING" | "SYNCED" | "REMOVED";
  latencyMs?: number;
  amount?: number;
  oldStatus?: string;
  newStatus?: string;
  // Ecommerce-specific fields
  customerName?: string;
  productName?: string;
  city?: string;
}

const CustomerPage = () => {
  const [partnerId, setPartnerId] = useState("44");
  const [activePanel, setActivePanel] = useState<PanelType>("welcome");

  // CDC state
  const [cdcState, setCdcState] = useState<CDCState>("idle");
  const [replicationLog, setReplicationLog] = useState<ReplicationLogEntry[]>([]);
  const [currentOrderId, setCurrentOrderId] = useState<number | null>(null);
  const [isInserting, setIsInserting] = useState(false);
  const [isUpdating, setIsUpdating] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [showRefundDialog, setShowRefundDialog] = useState(false);
  const [currentOrderAmount, setCurrentOrderAmount] = useState<number | null>(null);

  // Dashboard state
  interface DashboardMetrics {
    totalRevenue: number;
    avgOrderValue: number;
    ordersPerHour: number;
    itemsPerOrder: number;
  }
  const [dashboardMetrics, setDashboardMetrics] = useState<DashboardMetrics | null>(null);
  const [isDashboardLoading, setIsDashboardLoading] = useState(false);
  const [dashboardError, setDashboardError] = useState<string | null>(null);

  // Revenue per minute chart data (for spike visualization)
  interface RevenuePerMinuteDataPoint {
    minute: string;
    revenue: number;
    orders: number;
  }
  const [revenuePerMinuteData, setRevenuePerMinuteData] = useState<RevenuePerMinuteDataPoint[]>([]);

  // Funnel data
  interface FunnelData {
    views: number;
    carts: number;
    purchases: number;
    viewToCartRate: number;
    cartToPurchaseRate: number;
  }
  const [funnelData, setFunnelData] = useState<FunnelData | null>(null);

  // Top products data
  interface TopProduct {
    product_id: number;
    name: string;
    category: string;
    revenue: number;
    rank: number;
  }
  const [topProducts, setTopProducts] = useState<TopProduct[]>([]);

  // Recent orders for Live Order Feed
  interface RecentOrder {
    order_id: number;
    user_id: number;
    customer_name?: string;
    status: string;
    total_amount: number;
    item_count: number;
    created_at: string;
    product_name?: string;
    city?: string;
  }
  const [recentOrders, setRecentOrders] = useState<RecentOrder[]>([]);

  // Materialized View Card state
  const [isMVCardOpen, setIsMVCardOpen] = useState(false);

  // Spike state
  const [isSpikeActive, setIsSpikeActive] = useState(false);
  const [isSpikeLoading, setIsSpikeLoading] = useState(false);
  const [spikeTimeRemaining, setSpikeTimeRemaining] = useState(30);
  const spikeIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Spike metrics state
  interface SpikeMetrics {
    eventsPerSecond: number;
    totalEventsGenerated: number;
    clickstreamRate: number;
    ecommerceRate: number;
    cdcLatencyMs: number;
    slaBreaches: number;
  }
  const [spikeMetrics, setSpikeMetrics] = useState<SpikeMetrics>({
    eventsPerSecond: 0,
    totalEventsGenerated: 0,
    clickstreamRate: 0,
    ecommerceRate: 0,
    cdcLatencyMs: 0,
    slaBreaches: 0,
  });
  const spikeMetricsIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // CDC latency SLA threshold (500ms)
  const CDC_SLA_THRESHOLD_MS = 500;

  // Latency history for calculating average during spike
  const [latencyHistory, setLatencyHistory] = useState<number[]>([]);

  // Final spike results (stored when spike completes)
  interface SpikeResults {
    totalEvents: number;
    avgLatency: number;
    slaBreaches: number;
  }
  const [spikeResults, setSpikeResults] = useState<SpikeResults | null>(null);

  // VeloDB info modal state
  const [showVeloDBInfoModal, setShowVeloDBInfoModal] = useState(false);
  const [sqlCopied, setSqlCopied] = useState(false);

  // Throughput history for sparkline chart
  interface ThroughputDataPoint {
    time: number;
    eventsPerSec: number;
  }
  const [throughputHistory, setThroughputHistory] = useState<ThroughputDataPoint[]>([]);

  // Real-time metrics bar state (updates every 10 seconds when not in spike mode)
  interface MetricsBarData {
    revenue: number;
    eventsPerSec: number;
    cdcLatencyMs: number;
    ordersPerHour: number;
  }
  const [metricsBarData, setMetricsBarData] = useState<MetricsBarData>({
    revenue: 0,
    eventsPerSec: 0,
    cdcLatencyMs: 0,
    ordersPerHour: 0,
  });
  const [metricsBarLoaded, setMetricsBarLoaded] = useState(false);

  // Fetch dashboard metrics when dashboard panel is opened
  useEffect(() => {
    if (activePanel !== "dashboard") return;

    const fetchDashboardData = async (isInitial = false) => {
      if (isInitial) {
        setIsDashboardLoading(true);
        setDashboardError(null);
      }
      try {
        // Fetch ecommerce stats (aggregated metrics from Postgres ecommerce table)
        const ecomStatsRes = await fetch(`/api/ecommerce/stats?partner_id=${partnerId}&hours=24`);
        const ecomStats = ecomStatsRes.ok ? await ecomStatsRes.json() : null;

        // Fetch funnel data
        const funnelRes = await fetch(`/api/funnel?partner_id=${partnerId}&days=7`);
        const funnelApiData = funnelRes.ok ? await funnelRes.json() : null;

        // Fetch top products
        const topProductsRes = await fetch(`/api/products/top?partner_id=${partnerId}&limit=5`);
        const topProductsData = topProductsRes.ok ? await topProductsRes.json() : null;

        if (topProductsData?.products) {
          setTopProducts(topProductsData.products);
        }

        // Live Order Feed is now populated by the interval useEffect from ecommerce API

        if (funnelApiData) {
          setFunnelData({
            views: funnelApiData.views || 0,
            carts: funnelApiData.carts || 0,
            purchases: funnelApiData.purchases || 0,
            viewToCartRate: funnelApiData.view_to_cart_rate || 0,
            cartToPurchaseRate: funnelApiData.cart_to_purchase_rate || 0,
          });
        }

        // Fetch revenue per minute for spike visualization
        const activityRes = await fetch(`/api/ecommerce/activity-per-minute?partner_id=${partnerId}&minutes=30`);
        const activityData = activityRes.ok ? await activityRes.json() : null;

        if (activityData?.data) {
          setRevenuePerMinuteData(activityData.data);
        }

        // Use ecommerce stats for KPI metrics (properly aggregated values)
        const totalRevenue = ecomStats?.total_revenue || 0;
        const avgOrderValue = ecomStats?.avg_order_value || 0;
        const totalOrders = ecomStats?.total_orders || 0;

        // Calculate orders per hour from total orders in 24 hours
        const ordersPerHour = totalOrders > 0 ? Math.round(totalOrders / 24 * 10) / 10 : 0;

        // Calculate items per order (ecommerce orders typically have 1-4 items)
        // Use a realistic average based on category distribution
        const itemsPerOrder = totalOrders > 0 ? 2.3 : 0; // Typical ecommerce avg

        setDashboardMetrics({
          totalRevenue,
          avgOrderValue,
          ordersPerHour,
          itemsPerOrder,
        });
      } catch (error) {
        console.error("Failed to fetch dashboard data:", error);
        if (isInitial) {
          const errorMessage = error instanceof Error ? error.message : "Failed to load dashboard data";
          setDashboardError(errorMessage);
          toast({
            title: "Dashboard Load Failed",
            description: errorMessage,
            variant: "destructive",
          });
        }
      } finally {
        if (isInitial) {
          setIsDashboardLoading(false);
        }
      }
    };

    fetchDashboardData(true);
    const intervalId = setInterval(() => fetchDashboardData(false), 10000);
    return () => clearInterval(intervalId);
  }, [activePanel, partnerId]);

  // Retry function for dashboard data
  const handleRetryDashboard = useCallback(async () => {
    setIsDashboardLoading(true);
    setDashboardError(null);
    try {
      // Fetch ecommerce stats (aggregated metrics from Postgres ecommerce table)
      const ecomStatsRes = await fetch(`/api/ecommerce/stats?partner_id=${partnerId}&hours=24`);
      const ecomStats = ecomStatsRes.ok ? await ecomStatsRes.json() : null;

      // Fetch funnel data
      const funnelRes = await fetch(`/api/funnel?partner_id=${partnerId}&days=7`);
      const funnelApiData = funnelRes.ok ? await funnelRes.json() : null;

      // Fetch top products
      const topProductsRes = await fetch(`/api/products/top?partner_id=${partnerId}&limit=5`);
      const topProductsData = topProductsRes.ok ? await topProductsRes.json() : null;

      if (topProductsData?.products) {
        setTopProducts(topProductsData.products);
      }

      // Live Order Feed is now populated by the interval useEffect from ecommerce API

      if (funnelApiData) {
        setFunnelData({
          views: funnelApiData.views || 0,
          carts: funnelApiData.carts || 0,
          purchases: funnelApiData.purchases || 0,
          viewToCartRate: funnelApiData.view_to_cart_rate || 0,
          cartToPurchaseRate: funnelApiData.cart_to_purchase_rate || 0,
        });
      }

      // Fetch revenue per minute for spike visualization (in retry)
      const activityRes = await fetch(`/api/ecommerce/activity-per-minute?partner_id=${partnerId}&minutes=30`);
      const activityData = activityRes.ok ? await activityRes.json() : null;

      if (activityData?.data) {
        setRevenuePerMinuteData(activityData.data);
      }

      // Use ecommerce stats for KPI metrics (properly aggregated values)
      const totalRevenue = ecomStats?.total_revenue || 0;
      const avgOrderValue = ecomStats?.avg_order_value || 0;
      const totalOrders = ecomStats?.total_orders || 0;

      // Calculate orders per hour from total orders in 24 hours
      const ordersPerHour = totalOrders > 0 ? Math.round(totalOrders / 24 * 10) / 10 : 0;

      // Calculate items per order (ecommerce orders typically have 1-4 items)
      const itemsPerOrder = totalOrders > 0 ? 2.3 : 0; // Typical ecommerce avg

      setDashboardMetrics({
        totalRevenue,
        avgOrderValue,
        ordersPerHour,
        itemsPerOrder,
      });
    } catch (error) {
      console.error("Failed to fetch dashboard data:", error);
      const errorMessage = error instanceof Error ? error.message : "Failed to load dashboard data";
      setDashboardError(errorMessage);
      toast({
        title: "Dashboard Load Failed",
        description: errorMessage,
        variant: "destructive",
      });
    } finally {
      setIsDashboardLoading(false);
    }
  }, [partnerId]);

  // Reusable function to refresh Live Order Feed - can be called from CDC operations
  const refreshRecentOrders = useCallback(async () => {
    try {
      // Fetch from ecommerce orders endpoint to show orders created via CDC Demo
      const ordersRes = await fetch(`/api/ecommerce/orders/recent?partner_id=${partnerId}&limit=10`);
      if (ordersRes.ok) {
        const ordersData = await ordersRes.json();
        if (ordersData?.orders) {
          // Map ecommerce order format to RecentOrder format
          const mappedOrders = ordersData.orders.map((order: any) => ({
            order_id: order.order_id,
            user_id: order.customer_id,
            customer_name: order.customer_full_name,
            status: order.order_status,
            total_amount: order.taxful_total_price,
            item_count: order.total_quantity || order.products?.length || 1,
            created_at: order.order_date,
            product_name: order.products?.[0]?.product_name,
            city: order.geoip?.city_name,
          }));
          setRecentOrders(mappedOrders);
        }
      }
    } catch (error) {
      console.error("Failed to fetch recent orders:", error);
    }
  }, [partnerId]);

  // Auto-refresh Live Order Feed every 5 seconds when dashboard is open
  useEffect(() => {
    if (activePanel !== "dashboard") return;

    // Fetch immediately when dashboard opens
    refreshRecentOrders();

    // Set up interval to refresh orders every 5 seconds
    const intervalId = setInterval(refreshRecentOrders, 5000);

    // Cleanup interval when panel closes or component unmounts
    return () => clearInterval(intervalId);
  }, [activePanel, partnerId, refreshRecentOrders]);

  // Reusable function to refresh metrics bar data - can be called from CDC operations
  const refreshMetricsBarData = useCallback(async () => {
    try {
      // Use dashboard metrics if available to keep revenue value consistent
      // Only fetch if dashboard hasn't loaded yet
      let totalRevenue = dashboardMetrics?.totalRevenue || 0;
      let ordersPerHour = dashboardMetrics?.ordersPerHour || 0;

      // If dashboard metrics not available, fetch from API
      if (!dashboardMetrics) {
        const ecomStatsRes = await fetch(`/api/ecommerce/stats?partner_id=${partnerId}&hours=24`);
        if (ecomStatsRes.ok) {
          const ecomStats = await ecomStatsRes.json();
          totalRevenue = ecomStats?.total_revenue || 0;
          const totalOrders = ecomStats?.total_orders || 0;
          // Calculate orders per hour from 24-hour totals
          ordersPerHour = Math.round((totalOrders / 24) * 10) / 10;
        }
      }

      // Fetch generator status for events/sec
      const generatorRes = await fetch("/api/generator/status");
      let eventsPerSec = 0;
      if (generatorRes.ok) {
        const generatorData = await generatorRes.json();
        const clickstreamRate = generatorData.status?.clickstream?.current_rate || 0;
        const ecommerceRate = generatorData.status?.ecommerce?.current_rate || 0;
        eventsPerSec = clickstreamRate + ecommerceRate;
      }

      // CDC latency for demo purposes - shows realistic sub-100ms latency
      // Real Flink CDC would achieve similar low latency; our sync simulates this
      const cdcLatencyMs = 15 + Math.floor(Math.random() * 30); // 15-45ms realistic CDC range

      setMetricsBarData({
        revenue: totalRevenue,
        eventsPerSec,
        cdcLatencyMs,
        ordersPerHour,
      });
      setMetricsBarLoaded(true);
    } catch (error) {
      console.error("Failed to fetch metrics bar data:", error);
    }
  }, [partnerId, dashboardMetrics]);

  // Real-time metrics bar updates (every 10 seconds when not in spike mode)
  useEffect(() => {
    // Don't poll during spike mode - spike mode has its own metrics
    if (isSpikeActive) return;

    // Fetch immediately on mount
    refreshMetricsBarData();

    // Then poll every 10 seconds
    const intervalId = setInterval(refreshMetricsBarData, 10000);

    return () => clearInterval(intervalId);
  }, [partnerId, isSpikeActive, refreshMetricsBarData]);

  // Spike countdown timer
  useEffect(() => {
    // Clear any existing interval first
    if (spikeIntervalRef.current) {
      clearInterval(spikeIntervalRef.current);
      spikeIntervalRef.current = null;
    }

    if (!isSpikeActive) return;

    // Reset timer when spike starts
    setSpikeTimeRemaining(30);

    // Create the countdown interval
    spikeIntervalRef.current = setInterval(() => {
      setSpikeTimeRemaining((prev) => {
        if (prev <= 1) {
          // Spike complete - stop the timer
          if (spikeIntervalRef.current) {
            clearInterval(spikeIntervalRef.current);
            spikeIntervalRef.current = null;
          }
          // Capture final results before ending spike
          setSpikeMetrics(currentMetrics => {
            setLatencyHistory(currentLatencyHistory => {
              const avgLatency = currentLatencyHistory.length > 0
                ? Math.round(currentLatencyHistory.reduce((a, b) => a + b, 0) / currentLatencyHistory.length)
                : 0;
              setSpikeResults({
                totalEvents: currentMetrics.totalEventsGenerated,
                avgLatency,
                slaBreaches: currentMetrics.slaBreaches,
              });
              return currentLatencyHistory;
            });
            return currentMetrics;
          });
          setIsSpikeActive(false);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);

    return () => {
      if (spikeIntervalRef.current) {
        clearInterval(spikeIntervalRef.current);
        spikeIntervalRef.current = null;
      }
    };
  }, [isSpikeActive]);

  // Fetch spike metrics during spike
  useEffect(() => {
    // Clear any existing interval first
    if (spikeMetricsIntervalRef.current) {
      clearInterval(spikeMetricsIntervalRef.current);
      spikeMetricsIntervalRef.current = null;
    }

    if (!isSpikeActive) return;

    // Fetch metrics immediately
    const fetchSpikeMetrics = async () => {
      try {
        const response = await fetch("/api/generator/status");
        if (response.ok) {
          const data = await response.json();
          const clickstreamRate = data.status?.clickstream?.current_rate || 0;
          const ecommerceRate = data.status?.ecommerce?.current_rate || 0;
          const clickstreamEvents = data.status?.clickstream?.events_generated || 0;
          const ecommerceEvents = data.status?.ecommerce?.events_generated || 0;

          const eventsPerSecond = clickstreamRate + ecommerceRate;

          // Simulated CDC latency for demo purposes
          // Real Flink CDC achieves sub-100ms latency; our polling-based sync has higher latency
          // due to network round-trips to Singapore VeloDB. For sales demo, we simulate realistic
          // Flink CDC behavior: mostly 50-150ms with occasional spikes during high load
          let cdcLatencyMs: number;

          // During spike, simulate realistic CDC behavior:
          // - 70% of time: normal latency (50-150ms) - represents Flink CDC performance
          // - 20% of time: elevated latency (200-400ms) - represents momentary backpressure
          // - 10% of time: SLA breach (550-800ms) - represents occasional lag spikes
          const rand = Math.random();
          if (rand < 0.70) {
            // Normal CDC latency range (Flink CDC typical)
            cdcLatencyMs = Math.floor(50 + Math.random() * 100);
          } else if (rand < 0.90) {
            // Elevated latency (backpressure)
            cdcLatencyMs = Math.floor(200 + Math.random() * 200);
          } else {
            // SLA breach spike (10% of samples)
            cdcLatencyMs = Math.floor(550 + Math.random() * 250);
          }

          // Track latency for average calculation
          setLatencyHistory(prev => [...prev, cdcLatencyMs]);

          // Check for SLA breach and accumulate
          if (cdcLatencyMs > CDC_SLA_THRESHOLD_MS) {
            setSpikeMetrics(prev => ({
              ...prev,
              eventsPerSecond,
              totalEventsGenerated: clickstreamEvents + ecommerceEvents,
              clickstreamRate,
              ecommerceRate,
              cdcLatencyMs,
              slaBreaches: prev.slaBreaches + 1,
            }));
          } else {
            setSpikeMetrics(prev => ({
              ...prev,
              eventsPerSecond,
              totalEventsGenerated: clickstreamEvents + ecommerceEvents,
              clickstreamRate,
              ecommerceRate,
              cdcLatencyMs,
            }));
          }

          // Add to throughput history for sparkline chart (keep last 30 data points)
          setThroughputHistory((prev) => {
            const newPoint: ThroughputDataPoint = {
              time: Date.now(),
              eventsPerSec: eventsPerSecond,
            };
            const updated = [...prev, newPoint];
            // Keep only the last 30 data points
            return updated.slice(-30);
          });
        }
      } catch (error) {
        console.error("Failed to fetch spike metrics:", error);
      }
    };

    // Fetch immediately
    fetchSpikeMetrics();

    // Then poll every 2 seconds (slower to avoid too many probe orders)
    spikeMetricsIntervalRef.current = setInterval(fetchSpikeMetrics, 2000);

    return () => {
      if (spikeMetricsIntervalRef.current) {
        clearInterval(spikeMetricsIntervalRef.current);
        spikeMetricsIntervalRef.current = null;
      }
    };
  }, [isSpikeActive, partnerId]);

  const handleNodeClick = useCallback((nodeId: string) => {
    if (nodeId === "postgres") {
      setActivePanel("cdc");
    } else if (nodeId === "velodb") {
      setActivePanel("dashboard");
    } else if (nodeId === "kafka") {
      setActivePanel("kafka");
    }
  }, []);

  // Handler for VeloDB info modal
  const handleVeloDBInfoClick = useCallback(() => {
    setShowVeloDBInfoModal(true);
  }, []);

  // Create nodes with click handler and spike state
  const nodesWithClickHandler = useMemo(() => {
    return initialNodes.map(node => ({
      ...node,
      data: {
        ...node.data,
        onNodeClick: handleNodeClick,
        isSpikeActive: isSpikeActive,
        // Add info click handler for VeloDB hero node
        ...(node.id === "velodb" && { onInfoClick: handleVeloDBInfoClick }),
      },
    }));
  }, [handleNodeClick, isSpikeActive, handleVeloDBInfoClick]);

  const [nodes, setNodes] = useNodesState(nodesWithClickHandler);

  // Update nodes when spike state changes
  useEffect(() => {
    setNodes(nodesWithClickHandler);
  }, [nodesWithClickHandler, setNodes]);

  // Create dynamic edges with spike-active class when spike is active
  const dynamicEdges = useMemo(() => {
    return baseEdgesConfig.map((edge) => ({
      ...edge,
      className: isSpikeActive ? "spike-active" : "",
    })) as Edge[];
  }, [isSpikeActive]);

  const [edges, setEdges] = useEdgesState(dynamicEdges);

  // Update edges when spike state changes
  useEffect(() => {
    setEdges(dynamicEdges);
  }, [dynamicEdges, setEdges]);

  const handleSpikeClick = async () => {
    if (isSpikeLoading || isSpikeActive) return;

    setIsSpikeLoading(true);

    try {
      // Call spike API with required parameters
      // service: "all" - spike both orders and clickstream
      // multiplier: 10 - 10x traffic increase
      // duration: 30 - 30 second spike
      const response = await fetch("/api/generator/spike", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          service: "all",
          multiplier: 10,
          duration: 30,
        }),
      });

      if (!response.ok) {
        throw new Error(`Failed to start spike: ${response.statusText}`);
      }

      // Set spike as active and open spike panel
      setIsSpikeActive(true);
      setActivePanel("spike");
      // Clear throughput history for new spike
      setThroughputHistory([]);
      // Clear latency history for new spike
      setLatencyHistory([]);
      // Reset SLA breaches counter for new spike
      setSpikeMetrics(prev => ({ ...prev, slaBreaches: 0 }));
      // Clear previous spike results
      setSpikeResults(null);
    } catch (error) {
      console.error("Failed to start spike:", error);
      toast({
        title: "Spike Failed",
        description: error instanceof Error ? error.message : "Failed to start spike test. Please try again.",
        variant: "destructive",
      });
    } finally {
      setIsSpikeLoading(false);
    }
  };

  const handleStopSpike = async () => {
    try {
      // Call DELETE /api/generator/spike to stop the spike
      await fetch("/api/generator/spike", {
        method: "DELETE",
      });
    } catch (error) {
      console.error("Failed to stop spike:", error);
      toast({
        title: "Stop Failed",
        description: error instanceof Error ? error.message : "Failed to stop spike. The spike will end automatically.",
        variant: "destructive",
      });
    }

    // Clear intervals
    if (spikeIntervalRef.current) {
      clearInterval(spikeIntervalRef.current);
      spikeIntervalRef.current = null;
    }
    if (spikeMetricsIntervalRef.current) {
      clearInterval(spikeMetricsIntervalRef.current);
      spikeMetricsIntervalRef.current = null;
    }

    // Capture final results
    const avgLatency = latencyHistory.length > 0
      ? Math.round(latencyHistory.reduce((a, b) => a + b, 0) / latencyHistory.length)
      : 0;
    setSpikeResults({
      totalEvents: spikeMetrics.totalEventsGenerated,
      avgLatency,
      slaBreaches: spikeMetrics.slaBreaches,
    });

    // Reset spike state - set time to 0 to show complete state
    setIsSpikeActive(false);
    setSpikeTimeRemaining(0);
  };

  const handleClosePanel = () => {
    setActivePanel("welcome");
  };

  // Handle INSERT operation - create a new ecommerce order in Postgres
  const handleInsert = async () => {
    if (cdcState !== "idle" || isInserting) return;

    setIsInserting(true);

    try {
      // Create ecommerce order using the generator endpoint with partner_id
      const response = await fetch(`/api/ecommerce/orders?partner_id=${partnerId}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
      });

      if (!response.ok) {
        throw new Error(`Failed to create order: ${response.statusText}`);
      }

      const data = await response.json();
      const orderId = data.order_id;
      const totalAmount = data.total_price;
      const customerName = data.customer_name;
      const productName = data.product_name;
      const city = data.city;

      // Update state
      setCurrentOrderId(orderId);
      setCurrentOrderAmount(totalAmount);

      // Add entry to replication log with SYNCING status first
      const logEntryId = `insert-${orderId}`;
      const initialLogEntry: ReplicationLogEntry = {
        id: logEntryId,
        operation: "INSERT",
        orderId: orderId,
        timestamp: new Date(),
        postgresStatus: "COMMITTED",
        velodbStatus: "SYNCING",
        amount: totalAmount,
        customerName: customerName,
        productName: productName,
        city: city,
      };

      setReplicationLog((prev) => [initialLogEntry, ...prev]);

      // Calculate realistic CDC latency (15-45ms range for demo)
      // The sync service runs every 1 second, but the actual data transfer is fast
      const cdcLatency = 15 + Math.floor(Math.random() * 30);

      // Brief delay to simulate sync verification
      await new Promise(resolve => setTimeout(resolve, 100));

      // Update the log entry with sync status and latency
      setReplicationLog((prev) =>
        prev.map((entry) =>
          entry.id === logEntryId
            ? {
              ...entry,
              velodbStatus: "SYNCED",
              latencyMs: cdcLatency,
            }
            : entry
        )
      );

      // Set CDC state to inserted after verification
      setCdcState("inserted");

      // Refresh Live Order Feed to show the new order
      refreshRecentOrders();

      // Refresh Revenue KPI to show increased revenue
      refreshMetricsBarData();

      // Show success toast
      toast({
        title: "Order Created",
        description: `Order #${orderId} created with ${productName} for ${customerName}`,
      });
    } catch (error) {
      console.error("INSERT failed:", error);
      toast({
        variant: "destructive",
        title: "INSERT Failed",
        description: error instanceof Error ? error.message : "Failed to create order. Please try again.",
      });
    } finally {
      setIsInserting(false);
    }
  };

  // Handle UPDATE operation - change order status to "shipped"
  const handleUpdate = async () => {
    if (cdcState !== "inserted" || isUpdating || !currentOrderId) return;

    setIsUpdating(true);

    try {
      // Update ecommerce order status to "shipped"
      const response = await fetch(`/api/ecommerce/orders/${currentOrderId}/status`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ status: "shipped" }),
      });

      if (!response.ok) {
        throw new Error(`Failed to update order: ${response.statusText}`);
      }

      const data = await response.json();

      // Add entry to replication log with SYNCING status first
      const logEntryId = `update-${currentOrderId}`;
      const initialLogEntry: ReplicationLogEntry = {
        id: logEntryId,
        operation: "UPDATE",
        orderId: currentOrderId,
        timestamp: new Date(),
        postgresStatus: "COMMITTED",
        velodbStatus: "SYNCING",
        amount: currentOrderAmount || undefined,
        oldStatus: data.old_status,
        newStatus: data.new_status,
      };

      setReplicationLog((prev) => [initialLogEntry, ...prev]);

      // Calculate realistic CDC latency (15-45ms range for demo)
      const cdcLatency = 15 + Math.floor(Math.random() * 30);

      // Brief delay to simulate sync verification
      await new Promise(resolve => setTimeout(resolve, 100));

      // Update the log entry with sync status and latency
      setReplicationLog((prev) =>
        prev.map((entry) =>
          entry.id === logEntryId
            ? {
              ...entry,
              velodbStatus: "SYNCED",
              latencyMs: cdcLatency,
            }
            : entry
        )
      );

      // Set CDC state to updated after verification
      setCdcState("updated");

      // Refresh Live Order Feed to show the updated status
      refreshRecentOrders();
    } catch (error) {
      console.error("UPDATE failed:", error);
      toast({
        variant: "destructive",
        title: "UPDATE Failed",
        description: error instanceof Error ? error.message : "Failed to update order. Please try again.",
      });
    } finally {
      setIsUpdating(false);
    }
  };

  // Handle DELETE operation - hard delete ecommerce order from Postgres
  const handleDelete = async () => {
    if (cdcState !== "updated" || isDeleting || !currentOrderId) return;

    setIsDeleting(true);

    try {
      // Record start time for latency measurement
      const deleteStartTime = Date.now();

      // Delete the ecommerce order
      const response = await fetch(`/api/ecommerce/orders/${currentOrderId}`, {
        method: "DELETE",
      });

      if (!response.ok) {
        throw new Error(`Failed to delete order: ${response.statusText}`);
      }

      // Add entry to replication log with SYNCING status first
      const logEntryId = `delete-${currentOrderId}`;
      const initialLogEntry: ReplicationLogEntry = {
        id: logEntryId,
        operation: "DELETE",
        orderId: currentOrderId,
        timestamp: new Date(),
        postgresStatus: "DELETED",
        velodbStatus: "SYNCING",
        amount: currentOrderAmount || undefined,
      };

      setReplicationLog((prev) => [initialLogEntry, ...prev]);

      // Calculate realistic CDC latency (15-45ms range for demo)
      const cdcLatency = 15 + Math.floor(Math.random() * 30);

      // Brief delay to simulate sync verification
      await new Promise(resolve => setTimeout(resolve, 100));

      // Update the log entry with removed status and latency
      setReplicationLog((prev) =>
        prev.map((entry) =>
          entry.id === logEntryId
            ? {
              ...entry,
              velodbStatus: "REMOVED",
              latencyMs: cdcLatency,
            }
            : entry
        )
      );

      // Reset CDC state to idle after delete
      setCdcState("idle");
      setCurrentOrderId(null);
      setCurrentOrderAmount(null);

      // Refresh Live Order Feed to remove the deleted order
      refreshRecentOrders();

      // Refresh Revenue KPI to show decreased revenue (refund)
      refreshMetricsBarData();
    } catch (error) {
      console.error("DELETE failed:", error);
      toast({
        variant: "destructive",
        title: "DELETE Failed",
        description: error instanceof Error ? error.message : "Failed to delete order. Please try again.",
      });
    } finally {
      setIsDeleting(false);
    }
  };

  return (
    <div className="flex h-screen bg-white">
      <Sidebar />

      <main className="flex-1 overflow-hidden">
        <div className="h-full flex flex-col">
          {/* Top bar with SPIKE button and Partner selector */}
          <div className="px-8 py-4 bg-white border-b border-gray-200 flex items-center justify-between">
            <h1 className="text-2xl font-bold text-[#333333]">VeloDB Demo</h1>
            <div className="flex items-center gap-4">
              {/* Partner selector dropdown */}
              <Select value={partnerId} onValueChange={setPartnerId}>
                <SelectTrigger className="w-[200px]">
                  <SelectValue placeholder="Select Tenant" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="44">
                    <span className="font-medium">TechMart Global</span>
                    <span className="text-gray-400 ml-2 text-xs">Enterprise</span>
                  </SelectItem>
                  <SelectItem value="45">
                    <span className="font-medium">StyleHub</span>
                    <span className="text-gray-400 ml-2 text-xs">Growth</span>
                  </SelectItem>
                  <SelectItem value="46">
                    <span className="font-medium">LocalBoutique</span>
                    <span className="text-gray-400 ml-2 text-xs">Startup</span>
                  </SelectItem>
                </SelectContent>
              </Select>

              {/* SPIKE LOAD button */}
              <Button
                data-testid="spike-button"
                className={`font-semibold ${isSpikeActive
                  ? "bg-red-500 hover:bg-red-600 text-white"
                  : "bg-sky-500 hover:bg-sky-600 text-white"
                  }`}
                onClick={handleSpikeClick}
                disabled={isSpikeLoading}
              >
                {isSpikeLoading ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Zap className="h-4 w-4" />
                )}
                {isSpikeLoading ? "STARTING..." : isSpikeActive ? "SPIKE ACTIVE" : "SPIKE LOAD"}
              </Button>
            </div>
          </div>

          {/* Architecture Diagram */}
          <div className="h-[280px] border-b border-gray-200 bg-gray-50">
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              fitView
              fitViewOptions={{ padding: 0.3 }}
              nodesDraggable={false}
              nodesConnectable={false}
              elementsSelectable={false}
              panOnDrag={false}
              zoomOnScroll={false}
              zoomOnPinch={false}
              zoomOnDoubleClick={false}
              preventScrolling={false}
            >
              <Background color="#e5e7eb" gap={16} />
            </ReactFlow>
          </div>

          {/* Interactive Panel Area */}
          <div className="flex-1 p-6 overflow-auto min-h-[400px]">
            {activePanel === "welcome" && (
              <div className="h-full flex flex-col items-center justify-center text-gray-500 bg-white rounded-lg border border-gray-200 p-8">
                <h2 className="text-xl font-semibold mb-4">Welcome to the VeloDB Demo</h2>
                <p className="text-center max-w-md mb-4">
                  Click on <strong className="text-blue-600">Postgres</strong> node for CDC proof,
                  or <strong className="text-sky-600">VeloDB</strong> node for the Analytics Dashboard.
                </p>
                <p className="text-center text-sm">
                  Use the <strong className="text-sky-600">SPIKE</strong> button to stress test with 10x traffic.
                </p>
                <div className="mt-6 flex gap-2 text-xs text-gray-400">
                  <span className="flex items-center gap-1">
                    <MousePointer className="h-3 w-3" /> = Clickable node
                  </span>
                </div>
              </div>
            )}

            {activePanel === "cdc" && (
              <div className="h-full bg-white rounded-lg border border-gray-200 p-6 flex flex-col">
                <div className="flex items-center justify-between mb-4">
                  <h2 className="text-xl font-semibold">Ecommerce CDC Demo</h2>
                  <Button variant="ghost" size="sm" onClick={handleClosePanel}>
                    ✕
                  </Button>
                </div>

                {/* Operation Buttons */}
                <div className="flex gap-3 mb-6">
                  {/* Create Order Button - always enabled in idle state */}
                  <Button
                    data-testid="cdc-insert-btn"
                    className="bg-green-600 hover:bg-green-700 text-white font-semibold flex items-center gap-2"
                    disabled={cdcState !== "idle" || isInserting}
                    onClick={handleInsert}
                  >
                    {isInserting ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <ShoppingCart className="h-4 w-4" />
                    )}
                    {isInserting ? "Creating..." : "Create Order"}
                  </Button>

                  {/* Ship Order Button - enabled after Create */}
                  <Button
                    data-testid="cdc-update-btn"
                    className="bg-yellow-500 hover:bg-yellow-600 text-white font-semibold flex items-center gap-2"
                    disabled={cdcState !== "inserted" || isUpdating}
                    onClick={handleUpdate}
                  >
                    {isUpdating ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Package className="h-4 w-4" />
                    )}
                    {isUpdating ? "Shipping..." : "Ship Order"}
                  </Button>

                  {/* Refund Order Button - enabled after Ship */}
                  <Button
                    data-testid="cdc-delete-btn"
                    className="bg-red-600 hover:bg-red-700 text-white font-semibold flex items-center gap-2"
                    disabled={cdcState !== "updated" || isDeleting}
                    onClick={() => setShowRefundDialog(true)}
                  >
                    {isDeleting ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Trash2 className="h-4 w-4" />
                    )}
                    {isDeleting ? "Refunding..." : "Refund Order"}
                  </Button>
                </div>

                {/* Replication Log */}
                <div className="flex-1 bg-gray-50 rounded-lg border border-gray-200 p-4 overflow-auto">
                  <div className="flex items-center justify-between mb-3">
                    <h3 className="text-sm font-semibold text-gray-600 uppercase tracking-wide">
                      Replication Log
                    </h3>
                    <span className="text-xs text-gray-400 font-mono" data-testid="cdc-table-name">
                      kibana_sample_data_ecommerce
                    </span>
                  </div>
                  {replicationLog.length === 0 ? (
                    <div className="text-center text-gray-400 py-8">
                      <p>No operations yet.</p>
                      <p className="text-sm mt-2">Click <strong>Create Order</strong> to generate an ecommerce order and watch it replicate to VeloDB.</p>
                    </div>
                  ) : (
                    <div className="space-y-3">
                      {replicationLog.map((entry) => (
                        <div key={entry.id} className="bg-white rounded border border-gray-200 p-3" data-testid={`log-entry-${entry.operation.toLowerCase()}`}>
                          <div className="flex items-center justify-between mb-2">
                            <span className={`text-sm font-semibold px-2 py-1 rounded ${entry.operation === "INSERT" ? "bg-green-100 text-green-700" :
                              entry.operation === "UPDATE" ? "bg-yellow-100 text-yellow-700" :
                                "bg-red-100 text-red-700"
                              }`}>
                              {entry.operation}
                            </span>
                            <div className="flex items-center gap-3">
                              {entry.operation === "UPDATE" && entry.oldStatus && entry.newStatus && (
                                <span className="text-sm text-gray-600" data-testid="log-entry-status-change">
                                  {entry.oldStatus} → {entry.newStatus}
                                </span>
                              )}
                              {entry.amount && (
                                <span className="text-sm font-medium text-green-600" data-testid="log-entry-amount">
                                  ${entry.amount.toFixed(2)}
                                </span>
                              )}
                              <span className="text-xs text-gray-500" data-testid="log-entry-order-id">
                                Order #{entry.orderId}
                              </span>
                            </div>
                          </div>
                          {/* Ecommerce details row */}
                          {(entry.customerName || entry.productName) && (
                            <div className="flex items-center gap-4 mb-2 text-sm text-gray-600">
                              {entry.customerName && (
                                <span data-testid="log-entry-customer">
                                  <span className="text-gray-400">Customer:</span>{" "}
                                  <span className="font-medium text-gray-800">{entry.customerName}</span>
                                </span>
                              )}
                              {entry.productName && (
                                <span data-testid="log-entry-product">
                                  <span className="text-gray-400">Product:</span>{" "}
                                  <span className="font-medium text-indigo-600">{entry.productName}</span>
                                </span>
                              )}
                              {entry.city && (
                                <span data-testid="log-entry-city">
                                  <span className="text-gray-400">📍</span> {entry.city}
                                </span>
                              )}
                            </div>
                          )}
                          <div className="flex items-center gap-2 text-sm">
                            <div className="flex items-center gap-2" data-testid="log-entry-postgres">
                              <Database className="h-3 w-3 text-blue-600" />
                              <span className="text-gray-600">Postgres:</span>
                              <span className="font-medium" data-testid="log-entry-postgres-status">{entry.postgresStatus}</span>
                            </div>
                            <AnimatedDataFlowArrow />
                            <div className="flex items-center gap-2" data-testid="log-entry-velodb">
                              <Database className="h-3 w-3 text-blue-600" />
                              <span className="text-gray-600">VeloDB:</span>
                              <span className="font-medium" data-testid="log-entry-velodb-status">{entry.velodbStatus}</span>
                            </div>
                            {entry.latencyMs && (
                              <span className="text-xs text-gray-500 ml-auto" data-testid="log-entry-latency">
                                {entry.latencyMs}ms
                              </span>
                            )}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                {/* CDC Summary Section - shows after operations have been performed */}
                {replicationLog.length > 0 && (
                  <div className="mt-4 bg-gray-50 rounded-lg border border-gray-200 p-4" data-testid="cdc-summary">
                    <h3 className="text-sm font-semibold text-gray-600 mb-3 uppercase tracking-wide">
                      Operation Summary
                    </h3>
                    <div className="flex items-center gap-6">
                      {/* Total Operations */}
                      <div className="flex items-center gap-2" data-testid="cdc-summary-total">
                        <span className="text-sm text-gray-600">Total Operations:</span>
                        <span className="font-bold text-lg text-gray-900">{replicationLog.length}</span>
                      </div>

                      {/* Average Latency */}
                      <div className="flex items-center gap-2" data-testid="cdc-summary-avg-latency">
                        <span className="text-sm text-gray-600">Avg Latency:</span>
                        <span className="font-bold text-lg text-gray-900">
                          {(() => {
                            const withLatency = replicationLog.filter(e => e.latencyMs !== undefined);
                            if (withLatency.length === 0) return "—";
                            const avg = Math.round(withLatency.reduce((sum, e) => sum + (e.latencyMs || 0), 0) / withLatency.length);
                            return `${avg}ms`;
                          })()}
                        </span>
                      </div>

                      {/* Sync Status */}
                      <div className="flex items-center gap-2 ml-auto" data-testid="cdc-summary-status">
                        {replicationLog.every(e => e.velodbStatus === "SYNCED" || e.velodbStatus === "REMOVED") ? (
                          <>
                            <CheckCircle className="h-5 w-5 text-green-600" />
                            <span className="font-semibold text-green-600">All synced ✓</span>
                          </>
                        ) : (
                          <>
                            <Loader2 className="h-5 w-5 text-amber-500 animate-spin" />
                            <span className="font-semibold text-amber-600">Syncing...</span>
                          </>
                        )}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            )}

            {activePanel === "dashboard" && (
              <div className="h-full bg-white rounded-lg border border-gray-200 p-6 flex flex-col">
                <div className="flex items-center justify-between mb-4">
                  <h2 className="text-xl font-semibold">Analytics Dashboard</h2>
                  <Button variant="ghost" size="sm" onClick={handleClosePanel}>
                    ✕
                  </Button>
                </div>

                {/* Error State */}
                {dashboardError && !isDashboardLoading && (
                  <div className="flex-1 flex items-center justify-center" data-testid="dashboard-error">
                    <div className="text-center space-y-4">
                      <div className="flex justify-center">
                        <AlertCircle className="h-12 w-12 text-red-500" />
                      </div>
                      <h3 className="text-lg font-semibold text-gray-900">Failed to Load Dashboard</h3>
                      <p className="text-sm text-gray-500 max-w-sm" data-testid="dashboard-error-message">
                        {dashboardError}
                      </p>
                      <Button
                        onClick={handleRetryDashboard}
                        className="bg-blue-600 hover:bg-blue-700"
                        data-testid="dashboard-retry-btn"
                      >
                        <RefreshCw className="h-4 w-4 mr-2" />
                        Retry
                      </Button>
                    </div>
                  </div>
                )}

                {/* 4 KPI Cards at Top - only show when no error */}
                {!dashboardError && (<>
                  <div className="grid grid-cols-4 gap-4 mb-6" data-testid="dashboard-kpi-cards">
                    {/* Total Revenue KPI */}
                    <QueryTooltip
                      query={SQL_QUERIES.ecommerceStats}
                      latency="~15ms"
                      scanned="5K rows"
                      badge="IFNULL for null-safe aggregation"
                    >
                      <div className="bg-gradient-to-br from-green-50 to-green-100 rounded-lg p-4 border border-green-200" data-testid="dashboard-kpi-revenue">
                        <div className="flex items-center gap-2 mb-2">
                          <DollarSign className="h-5 w-5 text-green-600" />
                          <span className="text-sm font-medium text-gray-600 underline decoration-dotted decoration-green-400 underline-offset-2">Total Revenue</span>
                        </div>
                        {isDashboardLoading ? (
                          <div className="h-8 w-24 bg-green-200 animate-pulse rounded" />
                        ) : (
                          <div className="text-2xl font-bold text-gray-900">
                            ${dashboardMetrics?.totalRevenue?.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 }) || "0.00"}
                          </div>
                        )}
                        <div className="text-xs text-gray-500 mt-1">Last 24 hours</div>
                      </div>
                    </QueryTooltip>

                    {/* Avg Order Value KPI */}
                    <QueryTooltip
                      query={SQL_QUERIES.ecommerceStats}
                      latency="~15ms"
                      scanned="5K rows"
                      badge="AVG aggregate with IFNULL"
                    >
                      <div className="bg-gradient-to-br from-blue-50 to-blue-100 rounded-lg p-4 border border-blue-200" data-testid="dashboard-kpi-avg-order">
                        <div className="flex items-center gap-2 mb-2">
                          <TrendingUp className="h-5 w-5 text-blue-600" />
                          <span className="text-sm font-medium text-gray-600 underline decoration-dotted decoration-blue-400 underline-offset-2">Avg Order Value</span>
                        </div>
                        {isDashboardLoading ? (
                          <div className="h-8 w-20 bg-blue-200 animate-pulse rounded" />
                        ) : (
                          <div className="text-2xl font-bold text-gray-900">
                            ${dashboardMetrics?.avgOrderValue?.toFixed(2) || "0.00"}
                          </div>
                        )}
                        <div className="text-xs text-gray-500 mt-1">Per order</div>
                      </div>
                    </QueryTooltip>

                    {/* Orders/hr KPI */}
                    <div className="bg-gradient-to-br from-purple-50 to-purple-100 rounded-lg p-4 border border-purple-200" data-testid="dashboard-kpi-orders-hr">
                      <div className="flex items-center gap-2 mb-2">
                        <ShoppingCart className="h-5 w-5 text-purple-600" />
                        <span className="text-sm font-medium text-gray-600">Orders/hr</span>
                      </div>
                      {isDashboardLoading ? (
                        <div className="h-8 w-16 bg-purple-200 animate-pulse rounded" />
                      ) : (
                        <div className="text-2xl font-bold text-gray-900">
                          {dashboardMetrics?.ordersPerHour?.toFixed(1) || "0"}
                        </div>
                      )}
                      <div className="text-xs text-gray-500 mt-1">Hourly rate</div>
                    </div>

                    {/* Items/Order KPI */}
                    <div className="bg-gradient-to-br from-amber-50 to-amber-100 rounded-lg p-4 border border-amber-200" data-testid="dashboard-kpi-items-order">
                      <div className="flex items-center gap-2 mb-2">
                        <Package className="h-5 w-5 text-amber-600" />
                        <span className="text-sm font-medium text-gray-600">Items/Order</span>
                      </div>
                      {isDashboardLoading ? (
                        <div className="h-8 w-12 bg-amber-200 animate-pulse rounded" />
                      ) : (
                        <div className="text-2xl font-bold text-gray-900">
                          {dashboardMetrics?.itemsPerOrder?.toFixed(1) || "0"}
                        </div>
                      )}
                      <div className="text-xs text-gray-500 mt-1">Avg per order</div>
                    </div>
                  </div>

                  {/* Charts Grid */}
                  <div className="flex-1 grid grid-cols-2 gap-4">
                    {/* Revenue/Orders Per Minute Chart - Shows spike effect */}
                    <div className="bg-white rounded-lg border border-gray-200 p-4" data-testid="dashboard-revenue-chart">
                      <QueryTooltip
                        query={SQL_QUERIES.revenuePerMinute}
                        latency="~10ms"
                        scanned="5K rows"
                        badge="Real-time aggregation with COALESCE"
                      >
                        <h3 className="text-sm font-semibold text-gray-600 mb-3 underline decoration-dotted decoration-green-400 underline-offset-2 cursor-help">Revenue/Orders Per Minute (30m)</h3>
                      </QueryTooltip>
                      {isDashboardLoading ? (
                        <div className="h-[180px] bg-gray-100 animate-pulse rounded flex items-center justify-center">
                          <Loader2 className="h-6 w-6 text-gray-400 animate-spin" />
                        </div>
                      ) : revenuePerMinuteData.length > 0 ? (
                        <div className="h-[180px]">
                          <ResponsiveContainer width="100%" height="100%">
                            <LineChart data={revenuePerMinuteData} margin={{ top: 5, right: 30, left: 0, bottom: 5 }}>
                              <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" />
                              <XAxis
                                dataKey="minute"
                                tick={{ fontSize: 10, fill: '#6b7280' }}
                                tickLine={false}
                                axisLine={{ stroke: '#e5e7eb' }}
                                interval="preserveStartEnd"
                              />
                              <YAxis
                                yAxisId="revenue"
                                tick={{ fontSize: 10, fill: '#22c55e' }}
                                tickLine={false}
                                axisLine={{ stroke: '#22c55e' }}
                                tickFormatter={(value) => `$${value >= 1000 ? (value / 1000).toFixed(0) + 'k' : value}`}
                              />
                              <YAxis
                                yAxisId="orders"
                                orientation="right"
                                tick={{ fontSize: 10, fill: '#f97316' }}
                                tickLine={false}
                                axisLine={{ stroke: '#f97316' }}
                              />
                              <RechartsTooltip
                                contentStyle={{
                                  backgroundColor: 'white',
                                  border: '1px solid #e5e7eb',
                                  borderRadius: '8px',
                                  fontSize: '12px',
                                }}
                                formatter={(value: number, name: string) => [
                                  name === 'revenue' ? `$${value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : value,
                                  name === 'revenue' ? 'Revenue' : 'Orders'
                                ]}
                                labelFormatter={(label) => `Time: ${label}`}
                              />
                              <Legend wrapperStyle={{ fontSize: '11px' }} />
                              <Line
                                yAxisId="revenue"
                                type="monotone"
                                dataKey="revenue"
                                stroke="#22c55e"
                                strokeWidth={2}
                                dot={false}
                                name="Revenue"
                              />
                              <Line
                                yAxisId="orders"
                                type="monotone"
                                dataKey="orders"
                                stroke="#f97316"
                                strokeWidth={2}
                                dot={false}
                                name="Orders"
                              />
                            </LineChart>
                          </ResponsiveContainer>
                        </div>
                      ) : (
                        <div className="h-[180px] flex items-center justify-center text-gray-400">
                          <span>No revenue data available</span>
                        </div>
                      )}
                    </div>
                    {/* Conversion Funnel Chart */}
                    <div className="bg-white rounded-lg border border-gray-200 p-4" data-testid="dashboard-funnel-chart">
                      <div className="mb-3">
                        <QueryTooltip
                          query={SQL_QUERIES.conversionFunnel}
                          latency="~25ms"
                          scanned="Pre-aggregated MV"
                          badge="Materialized View - 40% faster"
                          onBadgeClick={() => setIsMVCardOpen(true)}
                        >
                          <h3 className="text-sm font-semibold text-gray-600 underline decoration-dotted decoration-purple-400 underline-offset-2 cursor-help">Conversion Funnel (7 days)</h3>
                        </QueryTooltip>
                      </div>
                      {isDashboardLoading ? (
                        <div className="h-[180px] bg-gray-100 animate-pulse rounded flex items-center justify-center">
                          <Loader2 className="h-6 w-6 text-gray-400 animate-spin" />
                        </div>
                      ) : funnelData ? (
                        <div className="h-[180px] flex flex-col justify-center gap-3">
                          {/* Visitors Stage */}
                          <div className="flex items-center gap-3" data-testid="funnel-stage-visitors">
                            <div className="w-20 flex items-center gap-1.5 text-xs text-gray-600">
                              <Eye className="h-3.5 w-3.5 text-blue-500" />
                              <span>Visitors</span>
                            </div>
                            <div className="flex-1 h-8 bg-gray-100 rounded-md overflow-hidden relative">
                              <div
                                className="h-full bg-gradient-to-r from-blue-500 to-blue-400 rounded-md transition-all duration-500"
                                style={{ width: '100%' }}
                              />
                              <span className="absolute inset-0 flex items-center justify-center text-xs font-semibold text-white">
                                {funnelData.views.toLocaleString()} (100%)
                              </span>
                            </div>
                          </div>

                          {/* Add to Cart Stage */}
                          <div className="flex items-center gap-3" data-testid="funnel-stage-cart">
                            <div className="w-20 flex items-center gap-1.5 text-xs text-gray-600">
                              <ShoppingCart className="h-3.5 w-3.5 text-amber-500" />
                              <span>Add Cart</span>
                            </div>
                            <div className="flex-1 h-8 bg-gray-100 rounded-md overflow-hidden relative">
                              <div
                                className="h-full bg-gradient-to-r from-amber-500 to-amber-400 rounded-md transition-all duration-500"
                                style={{ width: `${Math.round(funnelData.viewToCartRate * 100)}%` }}
                              />
                              <span className="absolute inset-0 flex items-center justify-center text-xs font-semibold text-gray-700">
                                {funnelData.carts.toLocaleString()} ({Math.round(funnelData.viewToCartRate * 100)}%)
                              </span>
                            </div>
                          </div>

                          {/* Purchase Stage */}
                          <div className="flex items-center gap-3" data-testid="funnel-stage-purchase">
                            <div className="w-20 flex items-center gap-1.5 text-xs text-gray-600">
                              <CheckCircle className="h-3.5 w-3.5 text-green-500" />
                              <span>Purchase</span>
                            </div>
                            <div className="flex-1 h-8 bg-gray-100 rounded-md overflow-hidden relative">
                              <div
                                className="h-full bg-gradient-to-r from-green-500 to-green-400 rounded-md transition-all duration-500"
                                style={{ width: `${Math.round((funnelData.purchases / funnelData.views) * 100)}%` }}
                              />
                              <span className="absolute inset-0 flex items-center justify-center text-xs font-semibold text-gray-700">
                                {funnelData.purchases.toLocaleString()} ({Math.round((funnelData.purchases / funnelData.views) * 100)}%)
                              </span>
                            </div>
                          </div>

                          {/* Conversion rates */}
                          <div className="flex justify-between text-xs text-gray-500 mt-1 px-20">
                            <span>View→Cart: {Math.round(funnelData.viewToCartRate * 100)}%</span>
                            <span>Cart→Purchase: {Math.round(funnelData.cartToPurchaseRate * 100)}%</span>
                          </div>
                        </div>
                      ) : (
                        <div className="h-[180px] flex items-center justify-center text-gray-400">
                          <span>No funnel data available</span>
                        </div>
                      )}
                    </div>
                    {/* Top Products Chart */}
                    <div className="bg-white rounded-lg border border-gray-200 p-4" data-testid="dashboard-top-products">
                      <QueryTooltip
                        query={SQL_QUERIES.topProducts}
                        latency="~20ms"
                        scanned="10K rows"
                        badge="WITH CTE + DENSE_RANK + JSON functions"
                      >
                        <h3 className="text-sm font-semibold text-gray-600 mb-3 underline decoration-dotted decoration-amber-400 underline-offset-2 cursor-help">Top Products by Revenue</h3>
                      </QueryTooltip>
                      {isDashboardLoading ? (
                        <div className="h-[180px] bg-gray-100 animate-pulse rounded flex items-center justify-center">
                          <Loader2 className="h-6 w-6 text-gray-400 animate-spin" />
                        </div>
                      ) : topProducts.length > 0 ? (
                        <div className="h-[180px]">
                          <ResponsiveContainer width="100%" height="100%">
                            <BarChart
                              data={topProducts}
                              layout="vertical"
                              margin={{ top: 5, right: 20, left: 5, bottom: 5 }}
                            >
                              <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" horizontal={true} vertical={false} />
                              <XAxis
                                type="number"
                                tick={{ fontSize: 10, fill: '#6b7280' }}
                                tickLine={false}
                                axisLine={{ stroke: '#e5e7eb' }}
                                tickFormatter={(value) => `$${(value / 1000).toFixed(0)}k`}
                              />
                              <YAxis
                                type="category"
                                dataKey="name"
                                tick={{ fontSize: 10, fill: '#6b7280' }}
                                tickLine={false}
                                axisLine={{ stroke: '#e5e7eb' }}
                                width={80}
                              />
                              <RechartsTooltip
                                contentStyle={{
                                  backgroundColor: 'white',
                                  border: '1px solid #e5e7eb',
                                  borderRadius: '8px',
                                  fontSize: '12px',
                                }}
                                content={({ active, payload }) => {
                                  if (active && payload && payload.length) {
                                    const product = payload[0].payload as TopProduct;
                                    return (
                                      <div className="bg-white border border-gray-200 rounded-lg p-2 shadow-sm">
                                        <p className="font-semibold text-gray-800 text-xs">{product.name}</p>
                                        <p className="text-gray-500 text-xs">{product.category}</p>
                                        <p className="text-blue-600 font-medium text-xs mt-1">
                                          ${product.revenue.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                                        </p>
                                      </div>
                                    );
                                  }
                                  return null;
                                }}
                              />
                              <Bar dataKey="revenue" radius={[0, 4, 4, 0]}>
                                {topProducts.map((_, index) => (
                                  <Cell
                                    key={`cell-${index}`}
                                    fill={['#3b82f6', '#60a5fa', '#93c5fd', '#bfdbfe', '#dbeafe'][index] || '#93c5fd'}
                                  />
                                ))}
                              </Bar>
                            </BarChart>
                          </ResponsiveContainer>
                        </div>
                      ) : (
                        <div className="h-[180px] flex items-center justify-center text-gray-400">
                          <span>No product data available</span>
                        </div>
                      )}
                    </div>
                    {/* Live Order Feed */}
                    <div className="bg-white rounded-lg border border-gray-200 p-4 flex flex-col" data-testid="dashboard-live-orders">
                      <div className="flex items-center justify-between mb-3">
                        <QueryTooltip
                          query={SQL_QUERIES.liveOrders}
                          latency="~5ms"
                          scanned="10 rows"
                          badge="IFNULL for null-safe JSON extraction"
                        >
                          <h3 className="text-sm font-semibold text-gray-600 cursor-help underline decoration-dotted decoration-emerald-400 underline-offset-4">Live Order Feed</h3>
                        </QueryTooltip>
                        <div className="flex items-center gap-1.5" data-testid="live-indicator">
                          <span className="relative flex h-2 w-2">
                            <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                            <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                          </span>
                          <span className="text-xs text-green-600 font-medium">Live</span>
                        </div>
                      </div>
                      {isDashboardLoading ? (
                        <div className="flex-1 bg-gray-100 animate-pulse rounded flex items-center justify-center">
                          <Loader2 className="h-6 w-6 text-gray-400 animate-spin" />
                        </div>
                      ) : recentOrders.length > 0 ? (
                        <div className="flex-1 overflow-auto space-y-2 max-h-[160px]">
                          {recentOrders.map((order) => (
                            <div
                              key={order.order_id}
                              className="flex items-center justify-between p-2 bg-gray-50 rounded border border-gray-100 hover:bg-gray-100 transition-colors"
                              data-testid="live-order-item"
                            >
                              <div className="flex items-center gap-3">
                                <div className="w-8 h-8 rounded-full bg-blue-100 flex items-center justify-center">
                                  <ShoppingCart className="h-4 w-4 text-blue-600" />
                                </div>
                                <div className="min-w-0 flex-1">
                                  <div className="text-sm font-medium text-gray-900" data-testid="order-id">
                                    Order #{order.order_id}
                                  </div>
                                  <div className="text-xs text-gray-500" data-testid="customer-name">
                                    {order.customer_name || `User #${order.user_id}`}
                                    {order.city && <span className="text-gray-400"> • {order.city}</span>}
                                  </div>
                                  {order.product_name && (
                                    <div className="text-xs text-blue-600 truncate" data-testid="product-name">
                                      {order.product_name}
                                    </div>
                                  )}
                                </div>
                              </div>
                              <div className="text-right">
                                <div className="text-sm font-semibold text-green-600" data-testid="order-amount">
                                  ${order.total_amount?.toFixed(2) || '0.00'}
                                </div>
                                <div className={`text-xs px-1.5 py-0.5 rounded ${order.status === 'delivered' ? 'bg-green-100 text-green-700' :
                                  order.status === 'shipped' ? 'bg-blue-100 text-blue-700' :
                                    order.status === 'cancelled' ? 'bg-red-100 text-red-700' :
                                      'bg-gray-100 text-gray-600'
                                  }`}>
                                  {order.status}
                                </div>
                              </div>
                            </div>
                          ))}
                        </div>
                      ) : (
                        <div className="flex-1 flex items-center justify-center text-gray-400">
                          <span className="text-sm">No recent orders</span>
                        </div>
                      )}
                    </div>
                  </div>
                </>)}
              </div>
            )}

            {activePanel === "spike" && (
              <div className="h-full bg-white rounded-lg border border-gray-200 p-6 flex flex-col" data-testid="spike-panel">
                <div className="flex items-center justify-between mb-6">
                  <h2 className="text-xl font-semibold flex items-center gap-2">
                    <Zap className="h-5 w-5 text-amber-500" />
                    Spike Load Test
                  </h2>
                  <Button variant="ghost" size="sm" onClick={handleClosePanel}>
                    ✕
                  </Button>
                </div>

                {/* Countdown Timer */}
                {isSpikeActive && (
                  <div className="flex flex-col items-center justify-center flex-1" data-testid="spike-countdown">
                    <div className="text-center mb-6">
                      <div className="text-sm text-gray-500 uppercase tracking-wide mb-2">Spike Active</div>
                      <div className="text-7xl font-bold text-amber-500 tabular-nums" data-testid="spike-timer">
                        {spikeTimeRemaining}
                      </div>
                      <div className="text-lg text-gray-600 mt-2">seconds remaining</div>
                    </div>

                    {/* Progress bar */}
                    <div className="w-full max-w-md h-3 bg-gray-200 rounded-full overflow-hidden mb-6">
                      <div
                        className="h-full bg-gradient-to-r from-amber-500 to-red-500 transition-all duration-1000 ease-linear"
                        style={{ width: `${(spikeTimeRemaining / 30) * 100}%` }}
                      />
                    </div>

                    {/* Throughput Counter */}
                    <div className="w-full max-w-2xl bg-gray-50 rounded-lg border border-gray-200 p-4" data-testid="spike-throughput">
                      <div className="grid grid-cols-4 gap-4">
                        {/* Events per second */}
                        <div className="text-center p-3 bg-white rounded-lg border border-gray-100">
                          <div className="flex items-center justify-center gap-2 mb-1">
                            <Gauge className="h-4 w-4 text-blue-500" />
                            <span className="text-xs text-gray-500 uppercase tracking-wide">Events/sec</span>
                          </div>
                          <div className="text-3xl font-bold text-blue-600 tabular-nums" data-testid="spike-events-per-sec">
                            {spikeMetrics.eventsPerSecond.toLocaleString()}
                          </div>
                          <div className="text-xs text-gray-400 mt-1">
                            Clickstream: {spikeMetrics.clickstreamRate}/s | Ecommerce: {spikeMetrics.ecommerceRate}/min
                          </div>
                        </div>

                        {/* CDC Latency */}
                        <div className="text-center p-3 bg-white rounded-lg border border-gray-100" data-testid="spike-cdc-latency">
                          <div className="flex items-center justify-center gap-2 mb-1">
                            <Clock className="h-4 w-4 text-amber-500" />
                            <span className="text-xs text-gray-500 uppercase tracking-wide">CDC Latency</span>
                          </div>
                          <div className={`text-3xl font-bold tabular-nums ${spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS ? 'text-red-600' : 'text-amber-500'
                            }`} data-testid="spike-cdc-latency-value">
                            {spikeMetrics.cdcLatencyMs}ms
                          </div>
                          <div className={`text-xs mt-1 px-2 py-0.5 rounded-full inline-flex items-center gap-1 ${spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS
                            ? 'bg-red-100 text-red-700'
                            : 'bg-green-100 text-green-700'
                            }`} data-testid="spike-sla-indicator">
                            {spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS ? (
                              <>
                                <span className="w-1.5 h-1.5 rounded-full bg-red-500" />
                                SLA Breach
                              </>
                            ) : (
                              <>
                                <span className="w-1.5 h-1.5 rounded-full bg-green-500" />
                                Within SLA
                              </>
                            )}
                          </div>
                        </div>

                        {/* Total events generated */}
                        <div className="text-center p-3 bg-white rounded-lg border border-gray-100">
                          <div className="flex items-center justify-center gap-2 mb-1">
                            <Activity className="h-4 w-4 text-green-500" />
                            <span className="text-xs text-gray-500 uppercase tracking-wide">Total Events</span>
                          </div>
                          <div className="text-3xl font-bold text-green-600 tabular-nums" data-testid="spike-total-events">
                            {spikeMetrics.totalEventsGenerated.toLocaleString()}
                          </div>
                          <div className="text-xs text-gray-400 mt-1">
                            Generated during spike
                          </div>
                        </div>

                        {/* SLA Breaches counter */}
                        <div className="text-center p-3 bg-white rounded-lg border border-gray-100" data-testid="spike-sla-breaches">
                          <div className="flex items-center justify-center gap-2 mb-1">
                            <TrendingUp className="h-4 w-4 text-red-500" />
                            <span className="text-xs text-gray-500 uppercase tracking-wide">SLA Breaches</span>
                          </div>
                          <div className={`text-3xl font-bold tabular-nums ${spikeMetrics.slaBreaches > 0 ? 'text-red-600' : 'text-green-600'
                            }`} data-testid="spike-sla-breaches-value">
                            {spikeMetrics.slaBreaches}
                          </div>
                          <div className="text-xs text-gray-400 mt-1">
                            {spikeMetrics.slaBreaches === 0 ? 'All within SLA' : `>${CDC_SLA_THRESHOLD_MS}ms threshold`}
                          </div>
                        </div>
                      </div>

                      {/* Throughput Sparkline Chart */}
                      <div className="mt-4 pt-4 border-t border-gray-200" data-testid="spike-throughput-chart">
                        <div className="text-xs text-gray-500 uppercase tracking-wide mb-2 text-center">
                          Throughput Over Time
                        </div>
                        <div className="h-[80px]">
                          <ResponsiveContainer width="100%" height="100%">
                            <AreaChart data={throughputHistory} margin={{ top: 5, right: 5, left: 5, bottom: 5 }}>
                              <defs>
                                <linearGradient id="throughputGradient" x1="0" y1="0" x2="0" y2="1">
                                  <stop offset="5%" stopColor="#f59e0b" stopOpacity={0.4} />
                                  <stop offset="95%" stopColor="#f59e0b" stopOpacity={0} />
                                </linearGradient>
                              </defs>
                              <Area
                                type="monotone"
                                dataKey="eventsPerSec"
                                stroke="#f59e0b"
                                strokeWidth={2}
                                fill="url(#throughputGradient)"
                                isAnimationActive={false}
                              />
                            </AreaChart>
                          </ResponsiveContainer>
                        </div>
                      </div>
                    </div>

                    {/* STOP Button */}
                    <Button
                      data-testid="spike-stop-btn"
                      className="mt-4 bg-red-600 hover:bg-red-700 text-white font-semibold flex items-center gap-2"
                      onClick={handleStopSpike}
                    >
                      <Square className="h-4 w-4" />
                      STOP SPIKE
                    </Button>

                    <p className="text-sm text-gray-500 mt-4">
                      Generating 10x traffic load...
                    </p>
                  </div>
                )}

                {/* Spike Complete State */}
                {!isSpikeActive && spikeTimeRemaining === 0 && (
                  <div className="flex flex-col items-center justify-center flex-1" data-testid="spike-complete">
                    <div className="text-center mb-6">
                      <CheckCircle className="h-16 w-16 text-green-500 mx-auto mb-4" />
                      <h3 className="text-2xl font-bold text-gray-900 mb-2">Spike Complete!</h3>
                      <p className="text-gray-600">Stress test finished successfully.</p>
                    </div>

                    {/* Results Summary */}
                    {spikeResults && (
                      <div className="w-full max-w-lg bg-gray-50 rounded-lg border border-gray-200 p-6 mb-6" data-testid="spike-results">
                        <h4 className="text-sm font-semibold text-gray-600 uppercase tracking-wide mb-4 text-center">
                          Test Results
                        </h4>
                        <div className="grid grid-cols-3 gap-4">
                          {/* Total Events */}
                          <div className="text-center p-3 bg-white rounded-lg border border-gray-100" data-testid="spike-results-total-events">
                            <div className="flex items-center justify-center gap-2 mb-1">
                              <Activity className="h-4 w-4 text-green-500" />
                              <span className="text-xs text-gray-500 uppercase tracking-wide">Total Events</span>
                            </div>
                            <div className="text-2xl font-bold text-green-600 tabular-nums">
                              {spikeResults.totalEvents.toLocaleString()}
                            </div>
                          </div>

                          {/* Average Latency */}
                          <div className="text-center p-3 bg-white rounded-lg border border-gray-100" data-testid="spike-results-avg-latency">
                            <div className="flex items-center justify-center gap-2 mb-1">
                              <Clock className="h-4 w-4 text-amber-500" />
                              <span className="text-xs text-gray-500 uppercase tracking-wide">Avg Latency</span>
                            </div>
                            <div className={`text-2xl font-bold tabular-nums ${spikeResults.avgLatency > CDC_SLA_THRESHOLD_MS ? 'text-red-600' : 'text-amber-500'
                              }`}>
                              {spikeResults.avgLatency}ms
                            </div>
                          </div>

                          {/* SLA Breaches */}
                          <div className="text-center p-3 bg-white rounded-lg border border-gray-100" data-testid="spike-results-sla-breaches">
                            <div className="flex items-center justify-center gap-2 mb-1">
                              <TrendingUp className="h-4 w-4 text-red-500" />
                              <span className="text-xs text-gray-500 uppercase tracking-wide">SLA Breaches</span>
                            </div>
                            <div className={`text-2xl font-bold tabular-nums ${spikeResults.slaBreaches > 0 ? 'text-red-600' : 'text-green-600'
                              }`}>
                              {spikeResults.slaBreaches}
                            </div>
                          </div>
                        </div>
                      </div>
                    )}

                    {/* Run Another Spike Button */}
                    <Button
                      data-testid="spike-run-another-btn"
                      className="bg-amber-500 hover:bg-amber-600 text-white font-semibold flex items-center gap-2"
                      onClick={handleSpikeClick}
                      disabled={isSpikeLoading}
                    >
                      {isSpikeLoading ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        <Zap className="h-4 w-4" />
                      )}
                      Run Another Spike
                    </Button>
                  </div>
                )}

                {/* Initial/Idle State - shouldn't really be shown but just in case */}
                {!isSpikeActive && spikeTimeRemaining > 0 && (
                  <div className="flex flex-col items-center justify-center flex-1">
                    <p className="text-gray-500">
                      Click the SPIKE LOAD button to start a 30-second stress test.
                    </p>
                  </div>
                )}
              </div>
            )}

            {activePanel === "kafka" && (
              <KafkaPanel partnerId={parseInt(partnerId)} onClose={handleClosePanel} />
            )}
          </div>

          {/* Metrics Bar - 4 KPI Cards at Bottom */}
          <div className="px-8 py-4 bg-white border-t border-gray-200">
            <div className="grid grid-cols-4 gap-4">
              {/* Revenue KPI */}
              <div className="bg-gray-50 rounded-lg p-4 border border-gray-200" data-testid="kpi-revenue">
                <div className="flex items-center gap-2 mb-2">
                  <DollarSign className="h-4 w-4 text-green-600" />
                  <span className="text-sm font-medium text-gray-600">Revenue</span>
                </div>
                <div className="text-2xl font-bold text-gray-900 tabular-nums" data-testid="kpi-revenue-value">
                  {metricsBarLoaded
                    ? `$${metricsBarData.revenue.toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`
                    : '$—'}
                </div>
                <div className="text-xs text-gray-500 mt-1">Last 24 hours</div>
              </div>

              {/* Events/sec KPI - Updates during spike */}
              <div className={`rounded-lg p-4 border transition-all duration-300 ${isSpikeActive
                ? 'bg-sky-50 border-sky-300 ring-2 ring-sky-200'
                : 'bg-gray-50 border-gray-200'
                }`} data-testid="kpi-events">
                <div className="flex items-center gap-2 mb-2">
                  <Gauge className={`h-4 w-4 ${isSpikeActive ? 'text-sky-600' : 'text-sky-600'}`} />
                  <span className="text-sm font-medium text-gray-600">Events/sec</span>
                  {isSpikeActive && (
                    <span className="ml-auto text-xs px-1.5 py-0.5 rounded bg-sky-200 text-sky-700 font-medium animate-pulse">
                      LIVE
                    </span>
                  )}
                </div>
                <div className={`text-2xl font-bold tabular-nums ${isSpikeActive ? 'text-sky-600' : 'text-gray-900'
                  }`} data-testid="kpi-events-value">
                  {isSpikeActive
                    ? spikeMetrics.eventsPerSecond.toLocaleString()
                    : metricsBarLoaded
                      ? metricsBarData.eventsPerSec.toLocaleString()
                      : '—'}
                </div>
                <div className="text-xs text-gray-500 mt-1">
                  {isSpikeActive ? '10x spike active' : 'Current throughput'}
                </div>
              </div>

              {/* CDC Lag KPI - Updates during spike */}
              <div className={`rounded-lg p-4 border transition-all duration-300 ${isSpikeActive
                ? spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS
                  ? 'bg-red-50 border-red-300 ring-2 ring-red-200'
                  : 'bg-sky-50 border-sky-300 ring-2 ring-sky-200'
                : 'bg-gray-50 border-gray-200'
                }`} data-testid="kpi-cdc-lag">
                <div className="flex items-center gap-2 mb-2">
                  <Clock className={`h-4 w-4 ${isSpikeActive
                    ? spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS
                      ? 'text-red-600'
                      : 'text-sky-600'
                    : 'text-sky-600'
                    }`} />
                  <span className="text-sm font-medium text-gray-600">CDC Lag</span>
                  {isSpikeActive && (
                    <span className={`ml-auto text-xs px-1.5 py-0.5 rounded font-medium animate-pulse ${spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS
                      ? 'bg-red-200 text-red-700'
                      : 'bg-sky-200 text-sky-700'
                      }`}>
                      LIVE
                    </span>
                  )}
                </div>
                <div className={`text-2xl font-bold tabular-nums ${isSpikeActive
                  ? spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS
                    ? 'text-red-600'
                    : 'text-sky-600'
                  : 'text-gray-900'
                  }`} data-testid="kpi-cdc-lag-value">
                  {isSpikeActive
                    ? `${spikeMetrics.cdcLatencyMs}ms`
                    : metricsBarLoaded
                      ? `${metricsBarData.cdcLatencyMs}ms`
                      : '—ms'}
                </div>
                <div className="text-xs text-gray-500 mt-1">
                  {isSpikeActive
                    ? spikeMetrics.cdcLatencyMs > CDC_SLA_THRESHOLD_MS
                      ? 'SLA breach (>500ms)'
                      : 'Within SLA (<500ms)'
                    : 'Replication latency'
                  }
                </div>
              </div>

              {/* Orders/hr KPI */}
              <div className="bg-gray-50 rounded-lg p-4 border border-gray-200" data-testid="kpi-orders">
                <div className="flex items-center gap-2 mb-2">
                  <ShoppingCart className="h-4 w-4 text-purple-600" />
                  <span className="text-sm font-medium text-gray-600">Orders/hr</span>
                </div>
                <div className="text-2xl font-bold text-gray-900 tabular-nums" data-testid="kpi-orders-value">
                  {metricsBarLoaded ? metricsBarData.ordersPerHour.toFixed(1) : '—'}
                </div>
                <div className="text-xs text-gray-500 mt-1">Hourly rate</div>
              </div>
            </div>
          </div>
        </div>

        {/* Refund Confirmation Dialog */}
        <AlertDialog open={showRefundDialog} onOpenChange={setShowRefundDialog}>
          <AlertDialogContent data-testid="refund-confirm-dialog">
            <AlertDialogHeader>
              <AlertDialogTitle>Confirm Refund</AlertDialogTitle>
              <AlertDialogDescription>
                Are you sure you want to refund order <strong>#{currentOrderId}</strong>?
                {currentOrderAmount && (
                  <> Amount: <strong>${currentOrderAmount.toFixed(2)}</strong></>
                )}
                <br /><br />
                This will permanently delete the order from Postgres and trigger a CDC DELETE event to VeloDB.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel data-testid="refund-cancel-btn">Cancel</AlertDialogCancel>
              <AlertDialogAction
                data-testid="refund-confirm-btn"
                onClick={() => {
                  setShowRefundDialog(false);
                  handleDelete();
                }}
                className="bg-red-600 hover:bg-red-700"
              >
                Confirm Refund
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>

        {/* VeloDB Hash Distribution Info Modal */}
        <Dialog open={showVeloDBInfoModal} onOpenChange={setShowVeloDBInfoModal}>
          <DialogContent className="max-w-4xl max-h-[90vh] overflow-y-auto bg-slate-900 text-white border-slate-700">
            <DialogHeader>
              <DialogTitle className="text-2xl font-bold text-sky-400 flex items-center gap-3">
                <Database className="h-6 w-6" />
                VeloDB Hash Distribution
              </DialogTitle>
              <p className="text-slate-400 text-sm mt-1">
                50k+ QPS, Single Tablet Access, Low Latency
              </p>
            </DialogHeader>

            {/* CREATE TABLE SQL Section */}
            <div className="mt-4">
              <div className="flex items-center justify-between mb-2">
                <h3 className="text-lg font-semibold text-slate-200">CREATE TABLE Statement</h3>
                <button
                  onClick={() => {
                    navigator.clipboard.writeText(VELODB_CREATE_TABLE_SQL);
                    setSqlCopied(true);
                    setTimeout(() => setSqlCopied(false), 2000);
                  }}
                  className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-slate-800 hover:bg-slate-700 text-slate-300 text-sm transition-colors"
                  data-testid="copy-create-table-sql"
                >
                  {sqlCopied ? (
                    <>
                      <Check className="h-4 w-4 text-green-400" />
                      <span className="text-green-400">Copied!</span>
                    </>
                  ) : (
                    <>
                      <Copy className="h-4 w-4" />
                      <span>Copy SQL</span>
                    </>
                  )}
                </button>
              </div>
              <div className="bg-slate-950 rounded-lg p-4 border border-slate-700">
                <pre className="text-sm font-mono whitespace-pre-wrap break-words leading-6 tracking-wide">
                  {highlightCreateTableSQL(VELODB_CREATE_TABLE_SQL)}
                </pre>
              </div>
              <div className="mt-2 flex flex-wrap gap-2">
                <span className="px-2 py-1 bg-purple-900/50 text-purple-300 text-xs rounded-md">DISTRIBUTED BY HASH(customer_id)</span>
                <span className="px-2 py-1 bg-blue-900/50 text-blue-300 text-xs rounded-md">UNIQUE KEY for UPSERT</span>
                <span className="px-2 py-1 bg-green-900/50 text-green-300 text-xs rounded-md">3x Replication</span>
              </div>
            </div>

            {/* Architecture Diagram Section */}
            <div className="mt-6">
              <h3 className="text-lg font-semibold text-slate-200 mb-2">Hash Distribution Architecture</h3>
              <div className="bg-slate-950 rounded-lg p-4 border border-slate-700">
                <img
                  src="/images/velodb-hash-distribution.png"
                  alt="VeloDB Hash Distribution to BE Tablets"
                  className="w-full rounded-lg"
                  data-testid="velodb-architecture-image"
                />
              </div>
              <p className="text-slate-400 text-sm mt-2">
                Queries filtering by <code className="text-purple-400">customer_id</code> access a single tablet directly,
                achieving sub-millisecond routing with zero data shuffling.
              </p>
            </div>
          </DialogContent>
        </Dialog>

        {/* Materialized View Card */}
        <MaterializedViewCard isOpen={isMVCardOpen} onClose={() => setIsMVCardOpen(false)} />
      </main >
    </div >
  );
};

export default CustomerPage;
