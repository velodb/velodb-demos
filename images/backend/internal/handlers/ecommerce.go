package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"velodb-demo/backend/internal/db"
)

type EcommerceHandler struct {
	postgres *db.PostgresClient
	velodb   *db.VeloDBClient
}

func NewEcommerceHandler(postgres *db.PostgresClient, velodb *db.VeloDBClient) *EcommerceHandler {
	return &EcommerceHandler{postgres: postgres, velodb: velodb}
}

// EcommerceOrderResponse represents an ecommerce order with parsed JSON fields
type EcommerceOrderResponse struct {
	ID                  int         `json:"id"`
	OrderID             int64       `json:"order_id"`
	OrderDate           string      `json:"order_date"`
	CustomerID          int64       `json:"customer_id"`
	CustomerFirstName   string      `json:"customer_first_name"`
	CustomerLastName    string      `json:"customer_last_name"`
	CustomerFullName    string      `json:"customer_full_name"`
	CustomerGender      string      `json:"customer_gender"`
	Email               string      `json:"email"`
	Currency            string      `json:"currency"`
	TaxfulTotalPrice    float64     `json:"taxful_total_price"`
	TaxlessTotalPrice   float64     `json:"taxless_total_price"`
	TotalQuantity       int         `json:"total_quantity"`
	TotalUniqueProducts int         `json:"total_unique_products"`
	OrderStatus         string      `json:"order_status"`
	Category            interface{} `json:"category"`
	Manufacturer        interface{} `json:"manufacturer"`
	Products            interface{} `json:"products"`
	GeoIP               interface{} `json:"geoip"`
}

func (h *EcommerceHandler) GetRecentOrders(w http.ResponseWriter, r *http.Request) {
	// Get partner_id for multi-tenant filtering
	partnerIDStr := r.URL.Query().Get("partner_id")
	partnerID := 44 // default to TechMart Global
	if partnerIDStr != "" {
		parsed, err := strconv.Atoi(partnerIDStr)
		if err == nil && parsed > 0 {
			partnerID = parsed
		}
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20 // default limit
	if limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	// Query VeloDB for multi-tenant ecommerce data
	orders, err := h.velodb.GetRecentEcommerceOrders(partnerID, limit)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch ecommerce orders")
		return
	}

	// Convert to response format with parsed JSON fields
	responseOrders := make([]EcommerceOrderResponse, len(orders))
	for i, order := range orders {
		responseOrders[i] = EcommerceOrderResponse{
			ID:                  order.ID,
			OrderID:             order.OrderID,
			OrderDate:           order.OrderDate.Format("2006-01-02T15:04:05Z"),
			CustomerID:          order.CustomerID,
			CustomerFirstName:   order.CustomerFirstName,
			CustomerLastName:    order.CustomerLastName,
			CustomerFullName:    order.CustomerFullName,
			CustomerGender:      order.CustomerGender,
			Email:               order.Email,
			Currency:            order.Currency,
			TaxfulTotalPrice:    order.TaxfulTotalPrice,
			TaxlessTotalPrice:   order.TaxlessTotalPrice,
			TotalQuantity:       order.TotalQuantity,
			TotalUniqueProducts: order.TotalUniqueProducts,
			OrderStatus:         order.OrderStatus,
		}

		// Parse JSON fields
		var category interface{}
		if err := json.Unmarshal([]byte(order.Category), &category); err == nil {
			responseOrders[i].Category = category
		} else {
			responseOrders[i].Category = []string{}
		}

		var manufacturer interface{}
		if err := json.Unmarshal([]byte(order.Manufacturer), &manufacturer); err == nil {
			responseOrders[i].Manufacturer = manufacturer
		} else {
			responseOrders[i].Manufacturer = []string{}
		}

		var products interface{}
		if err := json.Unmarshal([]byte(order.Products), &products); err == nil {
			responseOrders[i].Products = products
		} else {
			responseOrders[i].Products = []interface{}{}
		}

		var geoip interface{}
		if err := json.Unmarshal([]byte(order.GeoIP), &geoip); err == nil {
			responseOrders[i].GeoIP = geoip
		} else {
			responseOrders[i].GeoIP = map[string]interface{}{}
		}
	}

	response := map[string]interface{}{
		"orders": responseOrders,
		"count":  len(responseOrders),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetOrderByID returns a single ecommerce order by order_id
func (h *EcommerceHandler) GetOrderByID(w http.ResponseWriter, r *http.Request) {
	orderIDStr := chi.URLParam(r, "id")
	orderID, err := strconv.ParseInt(orderIDStr, 10, 64)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	order, err := h.postgres.GetEcommerceOrderByID(orderID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch ecommerce order")
		return
	}

	if order == nil {
		sendError(w, http.StatusNotFound, "order not found")
		return
	}

	// Convert to response format with parsed JSON fields
	response := EcommerceOrderResponse{
		ID:                  order.ID,
		OrderID:             order.OrderID,
		OrderDate:           order.OrderDate.Format("2006-01-02T15:04:05Z"),
		CustomerID:          order.CustomerID,
		CustomerFirstName:   order.CustomerFirstName,
		CustomerLastName:    order.CustomerLastName,
		CustomerFullName:    order.CustomerFullName,
		CustomerGender:      order.CustomerGender,
		Email:               order.Email,
		Currency:            order.Currency,
		TaxfulTotalPrice:    order.TaxfulTotalPrice,
		TaxlessTotalPrice:   order.TaxlessTotalPrice,
		TotalQuantity:       order.TotalQuantity,
		TotalUniqueProducts: order.TotalUniqueProducts,
		OrderStatus:         order.OrderStatus,
	}

	// Parse JSON fields
	var category interface{}
	if err := json.Unmarshal([]byte(order.Category), &category); err == nil {
		response.Category = category
	} else {
		response.Category = []string{}
	}

	var manufacturer interface{}
	if err := json.Unmarshal([]byte(order.Manufacturer), &manufacturer); err == nil {
		response.Manufacturer = manufacturer
	} else {
		response.Manufacturer = []string{}
	}

	var products interface{}
	if err := json.Unmarshal([]byte(order.Products), &products); err == nil {
		response.Products = products
	} else {
		response.Products = []interface{}{}
	}

	var geoip interface{}
	if err := json.Unmarshal([]byte(order.GeoIP), &geoip); err == nil {
		response.GeoIP = geoip
	} else {
		response.GeoIP = map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// UpdateOrderStatusRequest represents the request body for status update
type UpdateOrderStatusRequest struct {
	Status string `json:"status"`
}

// UpdateOrderStatus updates the status of an ecommerce order
func (h *EcommerceHandler) UpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	orderIDStr := chi.URLParam(r, "id")
	orderID, err := strconv.ParseInt(orderIDStr, 10, 64)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	var req UpdateOrderStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate status
	validStatuses := map[string]bool{
		"pending":    true,
		"processing": true,
		"shipped":    true,
		"delivered":  true,
		"cancelled":  true,
	}
	if !validStatuses[req.Status] {
		sendError(w, http.StatusBadRequest, "invalid status: must be pending, processing, shipped, delivered, or cancelled")
		return
	}

	// Get current order to verify it exists and get old status
	order, err := h.postgres.GetEcommerceOrderByID(orderID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch order")
		return
	}
	if order == nil {
		sendError(w, http.StatusNotFound, "order not found")
		return
	}

	oldStatus := order.OrderStatus

	// Update status
	if err := h.postgres.UpdateEcommerceOrderStatus(orderID, req.Status); err != nil {
		sendError(w, http.StatusInternalServerError, "failed to update order status")
		return
	}

	response := map[string]interface{}{
		"order_id":   orderID,
		"old_status": oldStatus,
		"new_status": req.Status,
		"success":    true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// DeleteOrder hard deletes an ecommerce order
func (h *EcommerceHandler) DeleteOrder(w http.ResponseWriter, r *http.Request) {
	orderIDStr := chi.URLParam(r, "id")
	orderID, err := strconv.ParseInt(orderIDStr, 10, 64)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid order ID")
		return
	}

	// Verify order exists before deletion
	order, err := h.postgres.GetEcommerceOrderByID(orderID)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch order")
		return
	}
	if order == nil {
		sendError(w, http.StatusNotFound, "order not found")
		return
	}

	// Delete the order
	if err := h.postgres.DeleteEcommerceOrder(orderID); err != nil {
		sendError(w, http.StatusInternalServerError, "failed to delete order")
		return
	}

	response := map[string]interface{}{
		"order_id": orderID,
		"deleted":  true,
		"success":  true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetActivityPerMinute returns revenue and orders per minute for spike visualization
// Supports partner_id and minutes query parameters
func (h *EcommerceHandler) GetActivityPerMinute(w http.ResponseWriter, r *http.Request) {
	partnerIDStr := r.URL.Query().Get("partner_id")
	partnerID := 44 // default
	if partnerIDStr != "" {
		parsed, err := strconv.Atoi(partnerIDStr)
		if err == nil && parsed > 0 {
			partnerID = parsed
		}
	}

	minutesStr := r.URL.Query().Get("minutes")
	minutes := 30 // default to 30 minutes
	if minutesStr != "" {
		parsed, err := strconv.Atoi(minutesStr)
		if err == nil && parsed > 0 && parsed <= 60 {
			minutes = parsed
		}
	}

	// Get revenue and orders per minute (ecommerce)
	revenueData, err := h.velodb.GetRevenuePerMinute(partnerID, minutes)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch revenue per minute")
		return
	}

	response := map[string]interface{}{
		"data":    revenueData,
		"minutes": minutes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetStats returns aggregated ecommerce statistics
// Supports partner_id query parameter for multi-tenant filtering
// Queries VeloDB for consistent multi-tenant analytics
func (h *EcommerceHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	// Get partner_id (required for multi-tenant filtering)
	partnerIDStr := r.URL.Query().Get("partner_id")
	partnerID := 44 // default to TechMart Global
	if partnerIDStr != "" {
		parsed, err := strconv.Atoi(partnerIDStr)
		if err == nil && parsed > 0 {
			partnerID = parsed
		}
	}

	hoursStr := r.URL.Query().Get("hours")
	hours := 24 // default
	if hoursStr != "" {
		parsed, err := strconv.Atoi(hoursStr)
		if err == nil && parsed > 0 && parsed <= 168 { // max 1 week
			hours = parsed
		}
	}

	// Query VeloDB for multi-tenant ecommerce stats
	stats, err := h.velodb.GetEcommerceStats(partnerID, hours)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to fetch ecommerce stats")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
