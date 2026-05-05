package models

import "time"

// RevenueMetrics represents hourly revenue metrics
type RevenueMetrics struct {
	Hour          time.Time `json:"hour"`
	Revenue       float64   `json:"revenue"`
	OrderCount    int       `json:"order_count"`
	AvgOrderValue float64   `json:"avg_order_value"`
	GrowthRate    float64   `json:"growth_rate"`
}

// ConversionFunnel represents the clickstream conversion funnel
type ConversionFunnel struct {
	PartnerID           int     `json:"partner_id"`
	PeriodDays          int     `json:"period_days"`
	Views               int64   `json:"views"`
	Carts               int64   `json:"carts"`
	Purchases           int64   `json:"purchases"`
	ViewToCartRate      float64 `json:"view_to_cart_rate"`
	CartToPurchaseRate  float64 `json:"cart_to_purchase_rate"`
}

// ProductIntelligence represents product ranking and performance
type ProductIntelligence struct {
	ProductID   int     `json:"product_id"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Revenue     float64 `json:"revenue"`
	Rank        int     `json:"rank"`
	PrevRank    *int    `json:"prev_rank,omitempty"`
	RankChange  int     `json:"rank_change"`
	GrowthRate  float64 `json:"growth_rate"`
}

// Order represents an order creation payload
type Order struct {
	OrderID   int         `json:"order_id,omitempty"`
	PartnerID int         `json:"partner_id"`
	UserID    int         `json:"user_id"`
	Items     []OrderItem `json:"items"`
	Status    string      `json:"status,omitempty"`
	CreatedAt time.Time   `json:"created_at,omitempty"`
}

// OrderItem represents a single item in an order
type OrderItem struct {
	ProductID int     `json:"product_id"`
	Quantity  int     `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}

// Partner represents partner metadata
type Partner struct {
	PartnerID   int    `json:"partner_id"`
	Name        string `json:"name"`
	Tier        string `json:"tier"`
	Status      string `json:"status"`
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status   string `json:"status"`
	VeloDB   string `json:"velodb"`
	Postgres string `json:"postgres"`
	Uptime   string `json:"uptime"`
}

// MetricsResponse represents server metrics response
type MetricsResponse struct {
	RequestsTotal      int64   `json:"requests_total"`
	ActiveConnections  int     `json:"active_connections"`
	AvgResponseTimeMs  float64 `json:"avg_response_time_ms"`
}

// RevenueMetricsResponse represents the revenue metrics API response
type RevenueMetricsResponse struct {
	PartnerID int              `json:"partner_id"`
	Metrics   []RevenueMetrics `json:"metrics"`
}

// ProductIntelligenceResponse represents the product intelligence API response
type ProductIntelligenceResponse struct {
	PartnerID int                   `json:"partner_id"`
	Products  []ProductIntelligence `json:"products"`
}

// OrderResponse represents the order creation response
type OrderResponse struct {
	OrderID     int     `json:"order_id"`
	Status      string  `json:"status"`
	TotalAmount float64 `json:"total_amount"`
}

// RecentOrder represents a simplified order for recent orders list
type RecentOrder struct {
	OrderID      int       `json:"order_id"`
	UserID       int       `json:"user_id"`
	CustomerName string    `json:"customer_name,omitempty"`
	TotalAmount  float64   `json:"total_amount"`
	Status       string    `json:"status"`
	OrderDate    time.Time `json:"order_date"`
}

// RecentClickstream represents a recent clickstream event
type RecentClickstream struct {
	EventID         string `json:"event_id"`
	EventType       string `json:"event_type"`
	UserID          int    `json:"user_id"`
	EventProperties string `json:"event_properties"`
	EventTimestamp  string `json:"event_timestamp"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// StatusResponse represents the comprehensive status check response
type StatusResponse struct {
	Status        string                   `json:"status"`
	Timestamp     time.Time                `json:"timestamp"`
	UptimeSeconds float64                  `json:"uptime_seconds"`
	Services      map[string]ServiceStatus `json:"services"`
}

// ServiceStatus represents the health status of a single service
type ServiceStatus struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}
