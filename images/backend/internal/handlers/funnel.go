package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"velodb-demo/backend/internal/db"
)

type FunnelHandler struct {
	velodb *db.VeloDBClient
}

func NewFunnelHandler(velodb *db.VeloDBClient) *FunnelHandler {
	return &FunnelHandler{velodb: velodb}
}

func (h *FunnelHandler) GetConversionFunnel(w http.ResponseWriter, r *http.Request) {
	partnerIDStr := r.URL.Query().Get("partner_id")
	daysStr := r.URL.Query().Get("days")

	if partnerIDStr == "" {
		sendError(w, http.StatusBadRequest, "partner_id is required")
		return
	}

	partnerID, err := strconv.Atoi(partnerIDStr)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid partner_id")
		return
	}

	days := 7
	if daysStr != "" {
		days, err = strconv.Atoi(daysStr)
		if err != nil {
			sendError(w, http.StatusBadRequest, "invalid days")
			return
		}
	}

	// Query the actual partner's funnel data - now with real per-partner conversion rates
	funnel, err := h.velodb.GetConversionFunnel(partnerID, days)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to retrieve conversion funnel")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(funnel)
}
