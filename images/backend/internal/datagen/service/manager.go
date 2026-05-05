package service

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

type Manager struct {
	config             *Config
	clickstreamService *ClickstreamService
	ecommerceService   *EcommerceService
	mu                 sync.RWMutex
}

func NewManager(config *Config) (*Manager, error) {
	m := &Manager{config: config}

	// Initialize clickstream service
	if config.Clickstream != nil && config.Clickstream.Enabled {
		svc, err := NewClickstreamService(config.Clickstream)
		if err != nil {
			return nil, fmt.Errorf("failed to create clickstream service: %w", err)
		}

		m.clickstreamService = svc
		log.Println("[Manager] Clickstream service initialized")
	}

	// Initialize ecommerce service
	if config.Ecommerce != nil && config.Ecommerce.Enabled {
		svc, err := NewEcommerceService(config.Ecommerce)
		if err != nil {
			return nil, fmt.Errorf("failed to create ecommerce service: %w", err)
		}
		m.ecommerceService = svc
		log.Println("[Manager] Ecommerce service initialized")
	}

	return m, nil
}

func (m *Manager) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Println("[Manager] Starting all generators...")

	// Use background context for long-running generators, not the request context
	bgCtx := context.Background()

	if m.clickstreamService != nil {
		if err := m.clickstreamService.Start(bgCtx); err != nil {
			return fmt.Errorf("failed to start clickstream service: %w", err)
		}
	}

	if m.ecommerceService != nil {
		if err := m.ecommerceService.Start(bgCtx); err != nil {
			return fmt.Errorf("failed to start ecommerce service: %w", err)
		}
	}

	log.Println("[Manager] All generators started successfully")
	return nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Println("[Manager] Stopping all generators...")

	if m.clickstreamService != nil {
		m.clickstreamService.Stop()
	}

	if m.ecommerceService != nil {
		m.ecommerceService.Stop()
	}

	log.Println("[Manager] All generators stopped")
	return nil
}

func (m *Manager) GetStatus() map[string]*Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]*Status)

	if m.clickstreamService != nil {
		status["clickstream"] = m.clickstreamService.GetStatus()
	}

	if m.ecommerceService != nil {
		status["ecommerce"] = m.ecommerceService.GetStatus()
	}

	return status
}

func (m *Manager) EnableSpike(service string, multiplier int, duration time.Duration) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	switch service {
	case "clickstream":
		if m.clickstreamService != nil {
			return m.clickstreamService.EnableSpike(multiplier, duration)
		}
		return fmt.Errorf("clickstream service not available")

	case "ecommerce":
		if m.ecommerceService != nil {
			return m.ecommerceService.EnableSpike(multiplier, duration)
		}
		return fmt.Errorf("ecommerce service not available")

	case "all":
		if m.clickstreamService != nil {
			m.clickstreamService.EnableSpike(multiplier, duration)
		}
		if m.ecommerceService != nil {
			m.ecommerceService.EnableSpike(multiplier, duration)
		}
		return nil

	default:
		return fmt.Errorf("unknown service: %s", service)
	}
}

func (m *Manager) DisableSpike(service string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	switch service {
	case "clickstream":
		if m.clickstreamService != nil {
			return m.clickstreamService.DisableSpike()
		}
		return fmt.Errorf("clickstream service not available")

	case "ecommerce":
		if m.ecommerceService != nil {
			return m.ecommerceService.DisableSpike()
		}
		return fmt.Errorf("ecommerce service not available")

	case "all":
		if m.clickstreamService != nil {
			m.clickstreamService.DisableSpike()
		}
		if m.ecommerceService != nil {
			m.ecommerceService.DisableSpike()
		}
		return nil

	default:
		return fmt.Errorf("unknown service: %s", service)
	}
}

func (m *Manager) SetRate(service string, rate int) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	switch service {
	case "clickstream":
		if m.clickstreamService != nil {
			return m.clickstreamService.SetRate(rate)
		}
		return fmt.Errorf("clickstream service not available")

	case "ecommerce":
		if m.ecommerceService != nil {
			return m.ecommerceService.SetRate(rate)
		}
		return fmt.Errorf("ecommerce service not available")

	default:
		return fmt.Errorf("unknown service: %s", service)
	}
}

func (m *Manager) UpdateConfig(config *Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update clickstream service config
	if config.Clickstream != nil && m.clickstreamService != nil {
		if err := m.clickstreamService.SetRate(config.Clickstream.BaselineRate); err != nil {
			return fmt.Errorf("failed to update clickstream service: %w", err)
		}
	}

	m.config = config
	log.Println("[Manager] Configuration updated")

	return nil
}

// CreateEcommerceOrder creates a single ecommerce order and returns its details
func (m *Manager) CreateEcommerceOrder(partnerID int) (*CreatedOrderResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ecommerceService == nil {
		return nil, fmt.Errorf("ecommerce service not available")
	}

	return m.ecommerceService.CreateSingleOrder(partnerID)
}

// CreateBatchOrders creates multiple ecommerce orders with historical timestamps
func (m *Manager) CreateBatchOrders(count int, backdateDays int, partnerID int, distribution string) (*BatchOrderResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.ecommerceService == nil {
		return nil, fmt.Errorf("ecommerce service not available")
	}

	return m.ecommerceService.CreateBatchOrders(count, backdateDays, partnerID, distribution)
}

// ResetDataResponse contains the reset operation results
type ResetDataResponse struct {
	TablesReset      []string `json:"tables_reset"`
	RowsDeleted      int64    `json:"rows_deleted"`
	GeneratorsStopped bool     `json:"generators_stopped"`
	ResetTimestamp   string   `json:"reset_timestamp"`
}

// ResetData truncates tables and resets service counters
func (m *Manager) ResetData(tables string, postgresDB, velodbDB interface{}) (*ResetDataResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf("[Manager] Resetting data for tables=%s", tables)

	// First stop generators
	if m.clickstreamService != nil {
		m.clickstreamService.Stop()
	}
	if m.ecommerceService != nil {
		m.ecommerceService.Stop()
	}

	response := &ResetDataResponse{
		TablesReset:      []string{},
		RowsDeleted:      0,
		GeneratorsStopped: true,
		ResetTimestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	// Reset database tables
	if tables == "all" || tables == "orders" {
		// TRUNCATE PostgreSQL orders table
		if pgDB, ok := postgresDB.(*sql.DB); ok && pgDB != nil {
			result, err := pgDB.Exec("TRUNCATE TABLE kibana_sample_data_ecommerce")
			if err != nil {
				log.Printf("[Manager] Failed to truncate PostgreSQL kibana_sample_data_ecommerce: %v", err)
			} else {
				rows, _ := result.RowsAffected()
				response.RowsDeleted += rows
				response.TablesReset = append(response.TablesReset, "postgres:kibana_sample_data_ecommerce")
				log.Printf("[Manager] Truncated PostgreSQL kibana_sample_data_ecommerce")
			}
		}

		// TRUNCATE VeloDB orders table
		if veloDBClient, ok := velodbDB.(*sql.DB); ok && veloDBClient != nil {
			result, err := veloDBClient.Exec("TRUNCATE TABLE kibana_sample_data_ecommerce")
			if err != nil {
				log.Printf("[Manager] Failed to truncate VeloDB kibana_sample_data_ecommerce: %v", err)
			} else {
				rows, _ := result.RowsAffected()
				response.RowsDeleted += rows
				response.TablesReset = append(response.TablesReset, "velodb:kibana_sample_data_ecommerce")
				log.Printf("[Manager] Truncated VeloDB kibana_sample_data_ecommerce")
			}
		}
	}

	if tables == "all" || tables == "clickstream" {
		// TRUNCATE VeloDB clickstream table
		if veloDBClient, ok := velodbDB.(*sql.DB); ok && veloDBClient != nil {
			result, err := veloDBClient.Exec("TRUNCATE TABLE fact_clickstream")
			if err != nil {
				log.Printf("[Manager] Failed to truncate VeloDB fact_clickstream: %v", err)
			} else {
				rows, _ := result.RowsAffected()
				response.RowsDeleted += rows
				response.TablesReset = append(response.TablesReset, "velodb:fact_clickstream")
				log.Printf("[Manager] Truncated VeloDB fact_clickstream")
			}
		}
	}

	// Reset service counters
	if m.ecommerceService != nil {
		if err := m.ecommerceService.Reset(); err != nil {
			log.Printf("[Manager] Failed to reset ecommerce service: %v", err)
		}
	}

	if m.clickstreamService != nil {
		if err := m.clickstreamService.Reset(); err != nil {
			log.Printf("[Manager] Failed to reset clickstream service: %v", err)
		}
	}

	log.Printf("[Manager] Data reset complete: %d rows deleted from %d tables", response.RowsDeleted, len(response.TablesReset))
	return response, nil
}
