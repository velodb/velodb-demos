/**
 * API Type Definitions for VeloDB Demo Backend
 * Matches backend response structures from worktree/agent-backend-core
 */

// ============================================================================
// Health & Monitoring
// ============================================================================

export interface HealthResponse {
  status: 'healthy' | 'unhealthy';
  velodb: 'connected' | 'disconnected';
  postgres: 'connected' | 'disconnected';
  uptime: string;
}

export interface MetricsResponse {
  requests_total: number;
  active_connections: number;
  avg_response_time_ms: number;
}

// ============================================================================
// Analytics - Revenue Metrics
// ============================================================================

export interface RevenueMetric {
  hour: string; // ISO 8601 timestamp
  revenue: number;
  order_count: number;
  avg_order_value: number;
  growth_rate: number;
}

export interface RevenueMetricsResponse {
  partner_id: number;
  metrics: RevenueMetric[];
}

// ============================================================================
// Analytics - Conversion Funnel
// ============================================================================

export interface FunnelResponse {
  partner_id: number;
  period_days: number;
  views: number;
  carts: number;
  purchases: number;
  view_to_cart_rate: number;
  cart_to_purchase_rate: number;
}

// ============================================================================
// Analytics - Product Intelligence
// ============================================================================

export interface ProductTop {
  product_id: number;
  name: string;
  revenue: number;
  rank: number;
  prev_rank: number;
  rank_change: number;
  growth_rate: number;
}

export interface TopProductsResponse {
  partner_id: number;
  products: ProductTop[];
}

// ============================================================================
// Order Management
// ============================================================================

export interface OrderItem {
  product_id: number;
  quantity: number;
  unit_price: number;
}

export interface CreateOrderRequest {
  partner_id: number;
  user_id: number;
  items: OrderItem[];
}

export interface CreateOrderResponse {
  order_id: number;
  status: 'created';
  total_amount: number;
}

export interface OrderResponse {
  order_id: number;
  partner_id: number;
  user_id: number;
  status: 'pending' | 'completed' | 'cancelled';
  created_at: string; // ISO 8601 timestamp
  items: OrderItem[];
}

// ============================================================================
// Data Generator Control
// ============================================================================

export interface GeneratorServiceStatus {
  running: boolean;
  current_rate: number;
  baseline_rate: number;
  spike_active: boolean;
  spike_ends_at: string;
  events_generated: number;
  errors: number;
  started_at: string;
}

export interface GeneratorStatus {
  orders: GeneratorServiceStatus;
  clickstream: GeneratorServiceStatus;
}

export interface GeneratorStatusResponse {
  status: GeneratorStatus;
}

export interface GeneratorMessageResponse {
  message: string;
  status: GeneratorStatus;
}

export interface TrafficSpikeRequest {
  service: 'orders' | 'clickstream' | 'all';
  multiplier: number; // 1-100
  duration: number; // 1-3600 seconds
}

export interface GeneratorRateRequest {
  service: 'orders' | 'clickstream';
  rate: number; // 1-10000
}

export interface GeneratorConfigRequest {
  orders?: {
    baseline_rate: number;
  };
  clickstream?: {
    baseline_rate: number;
  };
}

// ============================================================================
// Error Response
// ============================================================================

export interface ErrorResponse {
  error: string;
  message: string;
}

// ============================================================================
// Frontend Display Types (Derived from API responses)
// ============================================================================

/**
 * Transaction display format for PostgresPopout
 * Derived from OrderResponse
 */
export interface Transaction {
  id: number; // order_id
  time: string; // formatted created_at
  amt: string; // formatted total_amount
  status: 'COMPLETED' | 'PENDING' | 'RETURNED';
  // Additional display fields (may need separate fetch)
  customer?: string;
  items?: string;
  avatar?: string;
}

/**
 * Revenue metrics display format for VeloDBPopout
 */
export interface RevenueMetricsDisplay {
  currentHr: string; // formatted current hour revenue
  rolling24h: string; // formatted 24h rolling revenue
}

/**
 * SLA metrics display format
 */
export interface SLAMetrics {
  p95: number; // milliseconds
  breaches: number;
}

/**
 * Funnel step display format
 */
export interface FunnelStep {
  step: string; // e.g., "VISITORS", "ADD2CART", "CHECKOUT"
  count: number;
  pct: number; // percentage
}

/**
 * Product trend display format
 */
export interface ProductTrend {
  product: string; // product name
  category: string;
  growth: number; // growth rate percentage
  cartVal: string; // formatted revenue
}

/**
 * Clickstream event (for display in ClickstreamPopout)
 */
export interface ClickstreamEvent {
  event_type: string;
  partner_id: number;
  user_id?: number;
  product_id?: number;
  event_properties: Record<string, unknown>;
  event_time?: string;
}

// ============================================================================
// Recent Data - For Real-time Popouts
// ============================================================================

/**
 * Recent clickstream event from VeloDB
 * Matches backend: /api/clickstream/recent
 */
export interface RecentClickstreamEvent {
  event_id: string;
  event_type: string;
  user_id: number;
  event_properties: string; // JSON string
  event_timestamp: string;
}

export interface RecentClickstreamResponse {
  partner_id: number;
  events: RecentClickstreamEvent[];
}

/**
 * Recent order from Postgres
 * Matches backend: /api/orders/recent
 */
export interface RecentOrder {
  order_id: number;
  user_id: number;
  total_amount: number;
  status: string;
  order_date: string;
}

export interface RecentOrdersResponse {
  partner_id: number;
  orders: RecentOrder[];
}

// ============================================================================
// Ecommerce Orders (Kibana-compatible schema)
// ============================================================================

export interface EcommerceProduct {
  _id: string;
  product_id: number;
  product_name: string;
  category: string;
  manufacturer: string;
  sku: string;
  base_price: number;
  price: number;
  quantity: number;
  discount_percentage: number;
  discount_amount: number;
  taxful_price: number;
  taxless_price: number;
}

export interface EcommerceGeoip {
  city_name: string;
  continent_name: string;
  country_iso_code: string;
  region_name: string;
  location: {
    lat: number;
    lon: number;
  };
}

export interface EcommerceOrder {
  id: number;
  order_id: number;
  order_date: string;
  customer_id: number;
  customer_first_name: string;
  customer_last_name: string;
  customer_full_name: string;
  customer_gender: 'MALE' | 'FEMALE';
  email: string;
  currency: string;
  taxful_total_price: number;
  taxless_total_price: number;
  total_quantity: number;
  total_unique_products: number;
  order_status: string;
  category: string[];
  manufacturer: string[];
  products: EcommerceProduct[];
  geoip: EcommerceGeoip;
}

export interface EcommerceOrdersResponse {
  count: number;
  orders: EcommerceOrder[];
}
