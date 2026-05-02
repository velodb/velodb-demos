package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"velodb-demo/backend/internal/db"
)

type ClickstreamHandler struct {
	velodb *db.VeloDBClient
}

func NewClickstreamHandler(velodb *db.VeloDBClient) *ClickstreamHandler {
	return &ClickstreamHandler{velodb: velodb}
}

func (h *ClickstreamHandler) GetRecentClickstream(w http.ResponseWriter, r *http.Request) {
	partnerIDStr := r.URL.Query().Get("partner_id")
	if partnerIDStr == "" {
		sendError(w, http.StatusBadRequest, "partner_id is required")
		return
	}

	partnerID, err := strconv.Atoi(partnerIDStr)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid partner_id")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 50 // default limit
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 || limit > 100 {
			limit = 50
		}
	}

	events, err := h.velodb.GetRecentClickstream(partnerID, limit)
	if err != nil {
		log.Printf("[ERROR] GetRecentClickstream failed for partner %d: %v", partnerID, err)
		sendError(w, http.StatusInternalServerError, "failed to fetch clickstream events")
		return
	}

	response := map[string]interface{}{
		"partner_id": partnerID,
		"events":     events,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
