package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// EcommerceSyncService syncs kibana_sample_data_ecommerce from Postgres to VeloDB
type EcommerceSyncService struct {
	postgres *sql.DB
	velodb   *sql.DB

	ctx    context.Context
	cancel context.CancelFunc

	running   bool
	runningMu sync.RWMutex

	lastSyncTime  time.Time
	syncedOrders  int64
	updatedOrders int64
	deletedOrders int64
	errors        int64
	lastError     string
	syncMu        sync.RWMutex

	// Track last synced ID and timestamp for incremental sync
	lastSyncedID        int64
	lastSyncedUpdatedAt time.Time

	// Backfill tracking
	currentLag     int64
	syncMode       string
	lastLagCheck   time.Time
	backfillStart  time.Time
	backfillOrders int64
}

type EcommerceSyncStatus struct {
	Running       bool      `json:"running"`
	LastSyncTime  time.Time `json:"last_sync_time"`
	SyncedOrders  int64     `json:"synced_orders"`
	UpdatedOrders int64     `json:"updated_orders"`
	DeletedOrders int64     `json:"deleted_orders"`
	Errors        int64     `json:"errors"`
	LastError     string    `json:"last_error,omitempty"`
	// Backfill metrics
	CurrentLag       int64   `json:"current_lag"`
	SyncMode         string  `json:"sync_mode"`
	BackfillProgress float64 `json:"backfill_progress"`
	OrdersPerSecond  float64 `json:"orders_per_second"`
}

const (
	SyncModeIdle        = "idle"         // No new data
	SyncModeRealtime    = "realtime"     // Normal sync, small lag
	SyncModeFastForward = "fast_forward" // Backfill mode, large lag
)

func NewEcommerceSyncService(postgres, velodb *sql.DB) *EcommerceSyncService {
	return &EcommerceSyncService{
		postgres: postgres,
		velodb:   velodb,
		syncMode: SyncModeIdle,
	}
}

func (s *EcommerceSyncService) Start(ctx context.Context) error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if s.running {
		return fmt.Errorf("ecommerce sync service already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true

	// Initialize last synced timestamp
	s.lastSyncedUpdatedAt = time.Now().Add(-24 * time.Hour) // Start from 24h ago

	go s.syncLoop()

	log.Println("[EcommerceSync] Started")
	return nil
}

func (s *EcommerceSyncService) Stop() error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return nil
	}

	s.cancel()
	s.running = false

	log.Println("[EcommerceSync] Stopped")
	return nil
}

func (s *EcommerceSyncService) syncLoop() {
	// Initial sync
	s.performSync()

	ticker := time.NewTicker(1 * time.Second) // Fast sync for real-time demo
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.performSync()
		}
	}
}

func (s *EcommerceSyncService) performSync() {
	// Check lag every 10 seconds
	now := time.Now()
	if now.Sub(s.lastLagCheck) >= 10*time.Second {
		s.updateSyncLag()
		s.lastLagCheck = now
	}

	// Determine batch size based on lag
	batchSize := 500 // default
	if s.currentLag > 100 {  // Lower threshold for more aggressive backfill
		// Fast forward mode: larger batches
		batchSize = 5000
		if s.syncMode != SyncModeFastForward {
			s.syncMode = SyncModeFastForward
			s.backfillStart = now
			s.backfillOrders = 0
			log.Printf("[EcommerceSync] BACKFILL MODE: %d orders behind, batch size = %d", s.currentLag, batchSize)
		}
	} else if s.currentLag > 0 {
		// Realtime mode with normal batch size
		batchSize = 1000  // Increased from 500 for faster catch-up
		if s.syncMode != SyncModeRealtime {
			s.syncMode = SyncModeRealtime
			if s.backfillOrders > 0 {
				elapsed := now.Sub(s.backfillStart).Seconds()
				rate := float64(s.backfillOrders) / elapsed
				log.Printf("[EcommerceSync] Backfill complete! Synced %d orders in %.1fs (%.1f orders/sec)", s.backfillOrders, elapsed, rate)
				s.backfillOrders = 0
			}
			log.Printf("[EcommerceSync] Realtime mode: %d orders behind, batch size = %d", s.currentLag, batchSize)
		}
	} else {
		if s.syncMode != SyncModeIdle {
			s.syncMode = SyncModeIdle
		}
	}

	// Sync new orders
	newCount, err := s.syncNewOrdersWithBatch(batchSize)
	if err != nil {
		log.Printf("[EcommerceSync] Error syncing new orders: %v", err)
		s.recordError(err)
	}

	// Sync updated orders (for CDC demo)
	updateCount, err := s.syncUpdatedOrders()
	if err != nil {
		log.Printf("[EcommerceSync] Error syncing updated orders: %v", err)
		s.recordError(err)
	}

	// Sync deleted orders (CDC DELETE)
	deleteCount, err := s.syncDeletedOrders()
	if err != nil {
		log.Printf("[EcommerceSync] Error syncing deleted orders: %v", err)
		s.recordError(err)
	}

	s.syncMu.Lock()
	s.lastSyncTime = time.Now()
	s.syncedOrders += int64(newCount)
	s.updatedOrders += int64(updateCount)
	s.deletedOrders += int64(deleteCount)
	if s.syncMode == SyncModeFastForward {
		s.backfillOrders += int64(newCount)
	}
	s.syncMu.Unlock()

	if newCount > 0 || updateCount > 0 || deleteCount > 0 {
		if s.syncMode == SyncModeFastForward {
			log.Printf("[EcommerceSync] BACKFILL: Synced %d new orders (%d remaining)", newCount, s.currentLag)
		} else {
			log.Printf("[EcommerceSync] Synced %d new, %d updated, %d deleted orders", newCount, updateCount, deleteCount)
		}
	}
}

func (s *EcommerceSyncService) updateSyncLag() {
	var pgMax, veloMax int64

	err := s.postgres.QueryRow("SELECT COALESCE(MAX(order_id), 0) FROM kibana_sample_data_ecommerce").Scan(&pgMax)
	if err != nil {
		log.Printf("[EcommerceSync] Error getting Postgres max order_id: %v", err)
		return
	}

	err = s.velodb.QueryRow("SELECT COALESCE(MAX(order_id), 0) FROM kibana_sample_data_ecommerce").Scan(&veloMax)
	if err != nil {
		log.Printf("[EcommerceSync] Error getting VeloDB max order_id: %v", err)
		return
	}

	s.currentLag = pgMax - veloMax

	if s.currentLag > 100 {
		log.Printf("[EcommerceSync] Sync lag: %d orders (Postgres: %d, VeloDB: %d)", s.currentLag, pgMax, veloMax)
	}
}

func (s *EcommerceSyncService) syncNewOrdersWithBatch(batchSize int) (int, error) {
	// Get latest order_id from VeloDB
	var maxOrderID int64
	err := s.velodb.QueryRow("SELECT COALESCE(MAX(order_id), 0) FROM kibana_sample_data_ecommerce").Scan(&maxOrderID)
	if err != nil {
		return 0, fmt.Errorf("failed to get max order_id from VeloDB: %w", err)
	}

	// Query new orders from Postgres
	rows, err := s.postgres.Query(`
		SELECT id, order_date, order_id, partner_id, customer_id, customer_first_name, customer_last_name,
			   customer_full_name, customer_gender, customer_phone, email, "user",
			   day_of_week, day_of_week_i, currency,
			   taxful_total_price, taxless_total_price, total_quantity, total_unique_products,
			   order_status, type, category, manufacturer, sku, products, geoip, event,
			   created_at, updated_at
		FROM kibana_sample_data_ecommerce
		WHERE order_id > $1
		ORDER BY order_id
		LIMIT $2
	`, maxOrderID, batchSize)
	if err != nil {
		log.Printf("[EcommerceSync] ERROR: Failed to query Postgres: %v", err)
		return 0, fmt.Errorf("failed to query Postgres: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			id, orderID, customerID                                       int64
			partnerID                                                     sql.NullInt32
			orderDate                                                     time.Time
			customerFirstName, customerLastName, customerFullName         sql.NullString
			customerGender, customerPhone, email, user                    sql.NullString
			dayOfWeek                                                     sql.NullString
			dayOfWeekI                                                    sql.NullInt32
			currency                                                      sql.NullString
			taxfulTotalPrice, taxlessTotalPrice                           sql.NullFloat64
			totalQuantity, totalUniqueProducts                            sql.NullInt32
			orderStatus, orderType                                        sql.NullString
			category, manufacturer, sku, products, geoip, event           []byte
			createdAt, updatedAt                                          time.Time
		)

		if err := rows.Scan(
			&id, &orderDate, &orderID, &partnerID, &customerID, &customerFirstName, &customerLastName,
			&customerFullName, &customerGender, &customerPhone, &email, &user,
			&dayOfWeek, &dayOfWeekI, &currency,
			&taxfulTotalPrice, &taxlessTotalPrice, &totalQuantity, &totalUniqueProducts,
			&orderStatus, &orderType, &category, &manufacturer, &sku, &products, &geoip, &event,
			&createdAt, &updatedAt,
		); err != nil {
			log.Printf("[EcommerceSync] Failed to scan row: %v", err)
			continue
		}

		// Insert into VeloDB
		_, err := s.velodb.Exec(`
			INSERT INTO kibana_sample_data_ecommerce
				(order_date, order_id, partner_id, customer_id, customer_first_name, customer_last_name,
				 customer_full_name, customer_gender, customer_phone, email, user,
				 day_of_week, day_of_week_i, currency,
				 taxful_total_price, taxless_total_price, total_quantity, total_unique_products,
				 order_status, type, category, manufacturer, sku, products, geoip, event)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			orderDate, orderID, nullInt32(partnerID), customerID,
			nullString(customerFirstName), nullString(customerLastName), nullString(customerFullName),
			nullString(customerGender), nullString(customerPhone), nullString(email), nullString(user),
			nullString(dayOfWeek), nullInt32(dayOfWeekI), nullString(currency),
			nullFloat64(taxfulTotalPrice), nullFloat64(taxlessTotalPrice),
			nullInt32(totalQuantity), nullInt32(totalUniqueProducts),
			nullString(orderStatus), nullString(orderType),
			string(category), string(manufacturer), string(sku), string(products), string(geoip), string(event),
		)
		if err != nil {
			log.Printf("[EcommerceSync] Failed to insert order %d: %v", orderID, err)
			continue
		}
		count++
	}

	// ALWAYS log sync result (like clickstream)
	if count > 0 {
		log.Printf("[EcommerceSync] Loaded %d orders to VeloDB", count)
	}

	return count, nil
}

// Keep old method for compatibility, but redirect to new one
func (s *EcommerceSyncService) syncNewOrders() (int, error) {
	return s.syncNewOrdersWithBatch(500)
}

func (s *EcommerceSyncService) syncUpdatedOrders() (int, error) {
	// Query recently updated orders (for CDC demo - status changes)
	rows, err := s.postgres.Query(`
		SELECT order_id, order_status, updated_at
		FROM kibana_sample_data_ecommerce
		WHERE updated_at > $1 AND updated_at > created_at
		ORDER BY updated_at
		LIMIT 100
	`, s.lastSyncedUpdatedAt)
	if err != nil {
		return 0, fmt.Errorf("failed to query updated orders: %w", err)
	}
	defer rows.Close()

	count := 0
	var latestUpdatedAt time.Time

	for rows.Next() {
		var orderID int64
		var orderStatus string
		var updatedAt time.Time

		if err := rows.Scan(&orderID, &orderStatus, &updatedAt); err != nil {
			continue
		}

		// Update in VeloDB
		_, err := s.velodb.Exec(`
			UPDATE kibana_sample_data_ecommerce
			SET order_status = ?
			WHERE order_id = ?
		`, orderStatus, orderID)
		if err != nil {
			log.Printf("[EcommerceSync] Failed to update order %d: %v", orderID, err)
			continue
		}

		count++
		if updatedAt.After(latestUpdatedAt) {
			latestUpdatedAt = updatedAt
		}
	}

	if !latestUpdatedAt.IsZero() {
		s.lastSyncedUpdatedAt = latestUpdatedAt
	}

	return count, nil
}

func (s *EcommerceSyncService) syncDeletedOrders() (int, error) {
	// Get recent order_ids from VeloDB (last 1000 orders by order_id)
	rows, err := s.velodb.Query(`
		SELECT order_id FROM kibana_sample_data_ecommerce
		ORDER BY order_id DESC
		LIMIT 1000
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to query VeloDB order_ids: %w", err)
	}
	defer rows.Close()

	var velodbOrderIDs []int64
	for rows.Next() {
		var orderID int64
		if err := rows.Scan(&orderID); err != nil {
			continue
		}
		velodbOrderIDs = append(velodbOrderIDs, orderID)
	}

	if len(velodbOrderIDs) == 0 {
		return 0, nil
	}

	// Check which orders no longer exist in Postgres
	count := 0
	for _, orderID := range velodbOrderIDs {
		var exists int
		err := s.postgres.QueryRow(`
			SELECT 1 FROM kibana_sample_data_ecommerce WHERE order_id = $1
		`, orderID).Scan(&exists)

		if err == sql.ErrNoRows {
			// Order was deleted from Postgres, delete from VeloDB
			_, delErr := s.velodb.Exec(`
				DELETE FROM kibana_sample_data_ecommerce WHERE order_id = ?
			`, orderID)
			if delErr != nil {
				log.Printf("[EcommerceSync] Failed to delete order %d from VeloDB: %v", orderID, delErr)
				continue
			}
			count++
		}
	}

	return count, nil
}

func (s *EcommerceSyncService) recordError(err error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.errors++
	s.lastError = err.Error()
}

func (s *EcommerceSyncService) GetStatus() *EcommerceSyncStatus {
	s.runningMu.RLock()
	running := s.running
	s.runningMu.RUnlock()

	s.syncMu.RLock()
	defer s.syncMu.RUnlock()

	// Calculate orders per second for backfill progress
	var ordersPerSecond float64
	if s.syncMode == SyncModeFastForward && !s.backfillStart.IsZero() {
		elapsed := time.Since(s.backfillStart).Seconds()
		if elapsed > 0 {
			ordersPerSecond = float64(s.backfillOrders) / elapsed
		}
	}

	// Calculate backfill progress percentage
	var backfillProgress float64
	if s.currentLag > 0 {
		backfillProgress = 100.0 * (1.0 - float64(s.currentLag)/float64(s.currentLag+s.backfillOrders))
	}

	return &EcommerceSyncStatus{
		Running:          running,
		LastSyncTime:     s.lastSyncTime,
		SyncedOrders:     s.syncedOrders,
		UpdatedOrders:    s.updatedOrders,
		DeletedOrders:    s.deletedOrders,
		Errors:           s.errors,
		LastError:        s.lastError,
		CurrentLag:       s.currentLag,
		SyncMode:         s.syncMode,
		BackfillProgress: backfillProgress,
		OrdersPerSecond:  ordersPerSecond,
	}
}

// TriggerSync manually triggers an immediate sync
func (s *EcommerceSyncService) TriggerSync() {
	go s.performSync()
}

// Helper functions for null handling
func nullString(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullInt32(ni sql.NullInt32) interface{} {
	if ni.Valid {
		return ni.Int32
	}
	return 0
}

func nullFloat64(nf sql.NullFloat64) interface{} {
	if nf.Valid {
		return nf.Float64
	}
	return 0.0
}

// EcommerceOrder for JSON serialization (used by generator)
type EcommerceOrderDB struct {
	OrderDate           time.Time              `json:"order_date"`
	OrderID             int64                  `json:"order_id"`
	CustomerID          int64                  `json:"customer_id"`
	CustomerFirstName   string                 `json:"customer_first_name"`
	CustomerLastName    string                 `json:"customer_last_name"`
	CustomerFullName    string                 `json:"customer_full_name"`
	CustomerGender      string                 `json:"customer_gender"`
	CustomerPhone       string                 `json:"customer_phone"`
	Email               string                 `json:"email"`
	User                string                 `json:"user"`
	DayOfWeek           string                 `json:"day_of_week"`
	DayOfWeekI          int                    `json:"day_of_week_i"`
	Currency            string                 `json:"currency"`
	TaxfulTotalPrice    float64                `json:"taxful_total_price"`
	TaxlessTotalPrice   float64                `json:"taxless_total_price"`
	TotalQuantity       int                    `json:"total_quantity"`
	TotalUniqueProducts int                    `json:"total_unique_products"`
	OrderStatus         string                 `json:"order_status"`
	Type                string                 `json:"type"`
	Category            []string               `json:"category"`
	Manufacturer        []string               `json:"manufacturer"`
	SKU                 []string               `json:"sku"`
	Products            []map[string]interface{} `json:"products"`
	GeoIP               map[string]interface{} `json:"geoip"`
	Event               map[string]interface{} `json:"event"`
}

// InsertEcommerceOrder inserts an ecommerce order into Postgres
func InsertEcommerceOrder(db *sql.DB, order *EcommerceOrderDB) error {
	categoryJSON, _ := json.Marshal(order.Category)
	manufacturerJSON, _ := json.Marshal(order.Manufacturer)
	skuJSON, _ := json.Marshal(order.SKU)
	productsJSON, _ := json.Marshal(order.Products)
	geoipJSON, _ := json.Marshal(order.GeoIP)
	eventJSON, _ := json.Marshal(order.Event)

	_, err := db.Exec(`
		INSERT INTO kibana_sample_data_ecommerce
			(order_date, order_id, customer_id, customer_first_name, customer_last_name,
			 customer_full_name, customer_gender, customer_phone, email, "user",
			 day_of_week, day_of_week_i, currency,
			 taxful_total_price, taxless_total_price, total_quantity, total_unique_products,
			 order_status, type, category, manufacturer, sku, products, geoip, event)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)
	`,
		order.OrderDate, order.OrderID, order.CustomerID,
		order.CustomerFirstName, order.CustomerLastName, order.CustomerFullName,
		order.CustomerGender, order.CustomerPhone, order.Email, order.User,
		order.DayOfWeek, order.DayOfWeekI, order.Currency,
		order.TaxfulTotalPrice, order.TaxlessTotalPrice, order.TotalQuantity, order.TotalUniqueProducts,
		order.OrderStatus, order.Type,
		string(categoryJSON), string(manufacturerJSON), string(skuJSON),
		string(productsJSON), string(geoipJSON), string(eventJSON),
	)
	return err
}
