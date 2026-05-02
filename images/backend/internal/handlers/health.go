package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"velodb-demo/backend/internal/db"
	"velodb-demo/backend/internal/models"
)

type HealthHandler struct {
	velodb    *db.VeloDBClient
	postgres  *db.PostgresClient
	startTime time.Time
}

func NewHealthHandler(velodb *db.VeloDBClient, postgres *db.PostgresClient) *HealthHandler {
	return &HealthHandler{
		velodb:    velodb,
		postgres:  postgres,
		startTime: time.Now(),
	}
}

func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	velodbStatus := "connected"
	if err := h.velodb.Ping(); err != nil {
		velodbStatus = "disconnected"
	}

	postgresStatus := "connected"
	if err := h.postgres.Ping(); err != nil {
		postgresStatus = "disconnected"
	}

	status := "healthy"
	if velodbStatus == "disconnected" || postgresStatus == "disconnected" {
		status = "unhealthy"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	uptime := time.Since(h.startTime)

	response := models.HealthResponse{
		Status:   status,
		VeloDB:   velodbStatus,
		Postgres: postgresStatus,
		Uptime:   formatDuration(uptime),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *HealthHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	// For now, return dummy metrics
	// In production, you'd track these with prometheus or similar
	response := models.MetricsResponse{
		RequestsTotal:     0,
		ActiveConnections: 0,
		AvgResponseTimeMs: 0,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
