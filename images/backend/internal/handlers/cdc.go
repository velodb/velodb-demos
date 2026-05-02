package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"velodb-demo/backend/internal/db"
)

type CDCHandler struct {
	velodb   *db.VeloDBClient
	postgres *db.PostgresClient
}

func NewCDCHandler(velodb *db.VeloDBClient, postgres *db.PostgresClient) *CDCHandler {
	return &CDCHandler{
		velodb:   velodb,
		postgres: postgres,
	}
}

type CDCVerifyResponse struct {
	OrderID   int64 `json:"order_id"`
	Synced    bool  `json:"synced"`
	LatencyMs int64 `json:"latency_ms"`
}

// VerifySync checks if an ecommerce order has been synced from Postgres to VeloDB
// It polls VeloDB for up to 5 seconds waiting for the order to appear
// Works with kibana_sample_data_ecommerce table (order_id is int64)
func (h *CDCHandler) VerifySync(w http.ResponseWriter, r *http.Request) {
	// Extract order_id from URL path: /api/cdc/verify/{order_id}
	path := r.URL.Path
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		sendError(w, http.StatusBadRequest, "order_id is required")
		return
	}

	orderIDStr := parts[len(parts)-1]
	orderID, err := strconv.ParseInt(orderIDStr, 10, 64)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid order_id")
		return
	}

	// Get the ecommerce order creation time from Postgres for latency calculation
	pgOrder, err := h.postgres.GetEcommerceOrderByID(orderID)
	if err != nil || pgOrder == nil {
		sendError(w, http.StatusNotFound, "order not found in Postgres")
		return
	}

	startTime := time.Now()
	maxWait := 5 * time.Second
	pollInterval := 100 * time.Millisecond

	var synced bool
	var velodbOrderDate time.Time

	// Poll VeloDB waiting for the order to appear
	for time.Since(startTime) < maxWait {
		synced, velodbOrderDate, err = h.velodb.CheckOrderExists(int(orderID))
		if err != nil {
			sendError(w, http.StatusInternalServerError, "failed to check VeloDB")
			return
		}

		if synced {
			break
		}

		time.Sleep(pollInterval)
	}

	// Calculate latency
	var latencyMs int64
	if synced && !velodbOrderDate.IsZero() {
		// Use the difference between VeloDB order_date and Postgres order_date
		latencyMs = velodbOrderDate.Sub(pgOrder.OrderDate).Milliseconds()
		if latencyMs < 0 {
			// If negative (clock skew), use time since we started checking
			latencyMs = time.Since(startTime).Milliseconds()
		}
	} else if synced {
		// Fallback: use time since we started checking
		latencyMs = time.Since(startTime).Milliseconds()
	}

	response := CDCVerifyResponse{
		OrderID:   orderID,
		Synced:    synced,
		LatencyMs: latencyMs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
