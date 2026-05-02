package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"velodb-demo/backend/internal/db"
	"velodb-demo/backend/internal/models"
)

type ProductsHandler struct {
	velodb *db.VeloDBClient
}

func NewProductsHandler(velodb *db.VeloDBClient) *ProductsHandler {
	return &ProductsHandler{velodb: velodb}
}

func (h *ProductsHandler) GetTopProducts(w http.ResponseWriter, r *http.Request) {
	partnerIDStr := r.URL.Query().Get("partner_id")
	limitStr := r.URL.Query().Get("limit")

	if partnerIDStr == "" {
		sendError(w, http.StatusBadRequest, "partner_id is required")
		return
	}

	partnerID, err := strconv.Atoi(partnerIDStr)
	if err != nil {
		sendError(w, http.StatusBadRequest, "invalid partner_id")
		return
	}

	limit := 20
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil {
			sendError(w, http.StatusBadRequest, "invalid limit")
			return
		}
	}

	// Query products for the actual partner (now that we have multi-tenant data)
	products, err := h.velodb.GetProductIntelligence(partnerID, limit)
	if err != nil {
		sendError(w, http.StatusInternalServerError, "failed to retrieve product intelligence")
		return
	}

	response := models.ProductIntelligenceResponse{
		PartnerID: partnerID,
		Products:  products,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
