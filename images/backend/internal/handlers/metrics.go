package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"velodb-demo/backend/internal/db"
	"velodb-demo/backend/internal/models"
)

type MetricsHandler struct {
	velodb *db.VeloDBClient
}

func NewMetricsHandler(velodb *db.VeloDBClient) *MetricsHandler {
	return &MetricsHandler{velodb: velodb}
}

func (h *MetricsHandler) GetRevenueMetrics(w http.ResponseWriter, r *http.Request) {
	partnerIDStr := r.URL.Query().Get("partner_id")
	hoursStr := r.URL.Query().Get("hours")

	if partnerIDStr == "" {
		sendError(w, http.StatusBadRequest, "partner_id is required")
		return
	}

	partnerID, err := strconv.Atoi(partnerIDStr)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid partner_id")
		return
	}

	hours := 24
	if hoursStr != "" {
		hours, err = strconv.Atoi(hoursStr)
		if err != nil {
			sendError(w, http.StatusBadRequest, "invalid hours")
			return
		}
	}

	metrics, err := h.velodb.GetRevenueMetrics(partnerID, hours)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to retrieve revenue metrics")
		return
	}

	response := models.RevenueMetricsResponse{
		PartnerID: partnerID,
		Metrics:   metrics,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func sendError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(models.ErrorResponse{
		Error:   http.StatusText(code),
		Message: message,
	})
}
