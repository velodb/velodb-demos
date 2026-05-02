package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"velodb-demo/backend/internal/datagen/service"
)

type GeneratorHandler struct {
	manager      *service.Manager
	postgresDB   *sql.DB
	velodbDB     *sql.DB
}

func NewGeneratorHandler(manager *service.Manager) *GeneratorHandler {
	return &GeneratorHandler{
		manager: manager,
	}
}

// SetDatabases sets the database connections for reset operations
func (h *GeneratorHandler) SetDatabases(postgresDB, velodbDB *sql.DB) {
	h.postgresDB = postgresDB
	h.velodbDB = velodbDB
}

// StartRequest defines the request body for starting generators
type StartRequest struct {
	Services []string `json:"services"` // ["orders", "clickstream", "all"]
}

// StartGenerator handles POST /api/generator/start
func (h *GeneratorHandler) StartGenerator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.manager.Start(r.Context()); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Generators started successfully",
		"status":  h.manager.GetStatus(),
	})
}

// StopGenerator handles POST /api/generator/stop
func (h *GeneratorHandler) StopGenerator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.manager.Stop(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Generators stopped successfully",
		"status":  h.manager.GetStatus(),
	})
}

// GetStatus handles GET /api/generator/status
func (h *GeneratorHandler) GetStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": h.manager.GetStatus(),
	})
}

// SpikeRequest defines the request body for spike mode
type SpikeRequest struct {
	Service    string `json:"service"`    // "orders", "clickstream", "all"
	Multiplier int    `json:"multiplier"` // e.g., 10 for 10x rate
	Duration   int    `json:"duration"`   // duration in seconds
}

// EnableSpike handles POST /api/generator/spike
func (h *GeneratorHandler) EnableSpike(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SpikeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Validate multiplier
	if req.Multiplier < 1 || req.Multiplier > 100 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Multiplier must be between 1 and 100",
		})
		return
	}

	// Validate duration
	if req.Duration < 1 || req.Duration > 3600 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Duration must be between 1 and 3600 seconds",
		})
		return
	}

	duration := time.Duration(req.Duration) * time.Second
	if err := h.manager.EnableSpike(req.Service, req.Multiplier, duration); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Spike mode enabled",
		"status":  h.manager.GetStatus(),
	})
}

// DisableSpike handles DELETE /api/generator/spike
func (h *GeneratorHandler) DisableSpike(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get service from query parameter
	serviceName := r.URL.Query().Get("service")
	if serviceName == "" {
		serviceName = "all"
	}

	if err := h.manager.DisableSpike(serviceName); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Spike mode disabled",
		"status":  h.manager.GetStatus(),
	})
}

// SetRateRequest defines the request body for setting generation rate
type SetRateRequest struct {
	Service string `json:"service"` // "orders", "clickstream"
	Rate    int    `json:"rate"`    // new rate
}

// SetRate handles POST /api/generator/rate
func (h *GeneratorHandler) SetRate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SetRateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Validate rate
	if req.Rate < 1 || req.Rate > 10000 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Rate must be between 1 and 10000",
		})
		return
	}

	if err := h.manager.SetRate(req.Service, req.Rate); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Rate updated successfully",
		"status":  h.manager.GetStatus(),
	})
}

// UpdateConfigRequest defines the request body for updating configuration
type UpdateConfigRequest struct {
	Clickstream *ClickstreamConfigRequest `json:"clickstream,omitempty"`
}

type ClickstreamConfigRequest struct {
	BaselineRate int `json:"baseline_rate"` // events per second
}

// UpdateConfig handles POST /api/generator/config
func (h *GeneratorHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UpdateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Build service config from request
	config := &service.Config{}

	if req.Clickstream != nil {
		config.Clickstream = &service.ClickstreamConfig{
			BaselineRate: req.Clickstream.BaselineRate,
		}
	}

	if err := h.manager.UpdateConfig(config); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Configuration updated successfully",
		"status":  h.manager.GetStatus(),
	})
}

// CreateEcommerceOrder handles POST /api/ecommerce/orders - creates a single ecommerce order
func (h *GeneratorHandler) CreateEcommerceOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get partner_id from query parameter (default to 44)
	partnerID := 44
	if partnerIDStr := r.URL.Query().Get("partner_id"); partnerIDStr != "" {
		if parsed, err := strconv.Atoi(partnerIDStr); err == nil && parsed > 0 {
			partnerID = parsed
		}
	}

	order, err := h.manager.CreateEcommerceOrder(partnerID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	// Extract product name from products array
	productName := "Unknown Product"
	if len(order.Products) > 0 {
		if name, ok := order.Products[0]["product_name"].(string); ok {
			productName = name
		}
	}

	// Extract city from geoip
	city := ""
	if cityName, ok := order.GeoIP["city_name"].(string); ok {
		city = cityName
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id":      order.OrderID,
		"order_date":    order.OrderDate.Format(time.RFC3339),
		"customer_name": order.CustomerFullName,
		"product_name":  productName,
		"total_price":   order.TotalPrice,
		"currency":      order.Currency,
		"city":          city,
		"category":      order.Category,
		"order_status":  order.OrderStatus,
		"products":      order.Products,
		"geoip":         order.GeoIP,
	})
}

// BatchOrderRequest defines the request body for batch order creation
type BatchOrderRequest struct {
	Count         int    `json:"count"`          // Number of orders to create
	BackdateDays  int    `json:"backdate_days"`  // Number of days to backdate (0 = today only)
	PartnerID     int    `json:"partner_id"`     // Partner ID (0 = use 70/20/10 distribution)
	Distribution  string `json:"distribution"`   // "random" or "uniform"
}

// CreateBatchOrders handles POST /api/ecommerce/orders/batch
func (h *GeneratorHandler) CreateBatchOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req BatchOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Validate count
	if req.Count < 1 || req.Count > 10000 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Count must be between 1 and 10000",
		})
		return
	}

	// Validate backdate_days
	if req.BackdateDays < 0 || req.BackdateDays > 365 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Backdate days must be between 0 and 365",
		})
		return
	}

	// Default distribution to "random"
	if req.Distribution == "" {
		req.Distribution = "random"
	}

	// Create batch orders
	response, err := h.manager.CreateBatchOrders(req.Count, req.BackdateDays, req.PartnerID, req.Distribution)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ResetRequest defines the request body for data reset
type ResetRequest struct {
	Confirm bool `json:"confirm"` // Must be true to proceed
}

// ResetData handles DELETE /api/data/reset - truncates tables and resets service counters
func (h *GeneratorHandler) ResetData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body for confirmation
	var req ResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid request body: " + err.Error(),
		})
		return
	}

	// Require confirmation
	if !req.Confirm {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Confirmation required. Set 'confirm': true in request body",
		})
		return
	}

	// Get tables parameter (default: all)
	tables := r.URL.Query().Get("tables")
	if tables == "" {
		tables = "all"
	}

	// Validate tables parameter
	if tables != "all" && tables != "orders" && tables != "clickstream" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Invalid tables parameter. Must be 'all', 'orders', or 'clickstream'",
		})
		return
	}

	// Execute reset
	response, err := h.manager.ResetData(tables, h.postgresDB, h.velodbDB)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":             "success",
		"tables_reset":       response.TablesReset,
		"rows_deleted":       response.RowsDeleted,
		"generators_stopped": response.GeneratorsStopped,
		"reset_timestamp":    response.ResetTimestamp,
	})
}
