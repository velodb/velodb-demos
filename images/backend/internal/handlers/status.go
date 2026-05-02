package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"velodb-demo/backend/internal/db"
	"velodb-demo/backend/internal/models"
)

type StatusHandler struct {
	velodb     *db.VeloDBClient
	postgres   *db.PostgresClient
	startTime  time.Time
	httpClient *http.Client
}

func NewStatusHandler(velodb *db.VeloDBClient, postgres *db.PostgresClient, startTime time.Time) *StatusHandler {
	return &StatusHandler{
		velodb:    velodb,
		postgres:  postgres,
		startTime: startTime,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// coreServices defines which services are required for "healthy" status.
var coreServices = map[string]bool{
	"velodb":   true,
	"postgres": true,
	"redpanda": true,
}

func (h *StatusHandler) Status(w http.ResponseWriter, r *http.Request) {
	type result struct {
		name   string
		status models.ServiceStatus
	}

	checks := []struct {
		name string
		fn   func() models.ServiceStatus
	}{
		{"velodb", h.checkVeloDB},
		{"postgres", h.checkPostgres},
		{"redpanda", func() models.ServiceStatus { return h.checkHTTPService("http://redpanda:9644/v1/status/ready") }},
		{"frontend", func() models.ServiceStatus { return h.checkHTTPService("http://frontend:80") }},
		{"kibana", func() models.ServiceStatus { return h.checkHTTPService("http://kibana:5601/api/status") }},
		{"grafana", func() models.ServiceStatus { return h.checkHTTPService("http://grafana:3000/api/health") }},
		{"rag_api", func() models.ServiceStatus { return h.checkHTTPService("http://rag-api:8000/health") }},
		{"otel_proxy", func() models.ServiceStatus { return h.checkHTTPService("http://otel-proxy:8080") }},
	}

	results := make(chan result, len(checks))
	var wg sync.WaitGroup

	for _, c := range checks {
		wg.Add(1)
		go func(name string, fn func() models.ServiceStatus) {
			defer wg.Done()
			results <- result{name: name, status: fn()}
		}(c.name, c.fn)
	}

	wg.Wait()
	close(results)

	services := make(map[string]models.ServiceStatus, len(checks))
	for res := range results {
		services[res.name] = res.status
	}

	// Determine overall status
	overallStatus := "healthy"
	coreDown := false
	optionalDown := false

	for name, svc := range services {
		if svc.Status == "down" {
			if coreServices[name] {
				coreDown = true
			} else {
				optionalDown = true
			}
		}
	}

	if coreDown {
		overallStatus = "unhealthy"
	} else if optionalDown {
		overallStatus = "degraded"
	}

	resp := models.StatusResponse{
		Status:        overallStatus,
		Timestamp:     time.Now().UTC(),
		UptimeSeconds: time.Since(h.startTime).Seconds(),
		Services:      services,
	}

	if overallStatus == "unhealthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *StatusHandler) checkVeloDB() models.ServiceStatus {
	if h.velodb == nil {
		return models.ServiceStatus{Status: "down", Error: "not configured"}
	}
	start := time.Now()
	if err := h.velodb.Ping(); err != nil {
		return models.ServiceStatus{Status: "down", LatencyMs: time.Since(start).Milliseconds(), Error: err.Error()}
	}
	return models.ServiceStatus{Status: "up", LatencyMs: time.Since(start).Milliseconds()}
}

func (h *StatusHandler) checkPostgres() models.ServiceStatus {
	if h.postgres == nil {
		return models.ServiceStatus{Status: "down", Error: "not configured"}
	}
	start := time.Now()
	if err := h.postgres.Ping(); err != nil {
		return models.ServiceStatus{Status: "down", LatencyMs: time.Since(start).Milliseconds(), Error: err.Error()}
	}
	return models.ServiceStatus{Status: "up", LatencyMs: time.Since(start).Milliseconds()}
}

func (h *StatusHandler) checkHTTPService(url string) models.ServiceStatus {
	start := time.Now()
	resp, err := h.httpClient.Get(url)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return models.ServiceStatus{Status: "down", LatencyMs: latency, Error: err.Error()}
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return models.ServiceStatus{Status: "down", LatencyMs: latency, Error: resp.Status}
	}
	return models.ServiceStatus{Status: "up", LatencyMs: latency}
}
