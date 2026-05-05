package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

// SyncService syncs data from Postgres to VeloDB
type SyncService struct {
	postgres *sql.DB
	velodb   *sql.DB

	ctx    context.Context
	cancel context.CancelFunc

	running   bool
	runningMu sync.RWMutex

	lastSyncTime  time.Time
	syncedOrders  int64
	syncedItems   int64
	errors        int64
	lastError     string
	syncMu        sync.RWMutex
}

type SyncStatus struct {
	Running       bool      `json:"running"`
	LastSyncTime  time.Time `json:"last_sync_time"`
	SyncedOrders  int64     `json:"synced_orders"`
	SyncedItems   int64     `json:"synced_items"`
	Errors        int64     `json:"errors"`
	LastError     string    `json:"last_error,omitempty"`
}

func NewSyncService(postgres, velodb *sql.DB) *SyncService {
	return &SyncService{
		postgres: postgres,
		velodb:   velodb,
	}
}

func (s *SyncService) Start(ctx context.Context) error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if s.running {
		return fmt.Errorf("sync service already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true

	go s.syncLoop()

	log.Println("[SyncService] Started")
	return nil
}

func (s *SyncService) Stop() error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return nil
	}

	s.cancel()
	s.running = false

	log.Println("[SyncService] Stopped")
	return nil
}

func (s *SyncService) syncLoop() {
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

func (s *SyncService) performSync() {
	log.Println("[SyncService] Starting sync...")

	// Sync orders
	ordersCount, err := s.syncOrders()
	if err != nil {
		log.Printf("[SyncService] Error syncing orders: %v", err)
		s.recordError(err)
	} else if ordersCount > 0 {
		log.Printf("[SyncService] Synced %d orders", ordersCount)
	}

	// Sync order items
	itemsCount, err := s.syncOrderItems()
	if err != nil {
		log.Printf("[SyncService] Error syncing order items: %v", err)
		s.recordError(err)
	} else if itemsCount > 0 {
		log.Printf("[SyncService] Synced %d order items", itemsCount)
	}

	s.syncMu.Lock()
	s.lastSyncTime = time.Now()
	s.syncedOrders += int64(ordersCount)
	s.syncedItems += int64(itemsCount)
	s.syncMu.Unlock()
}

func (s *SyncService) syncOrders() (int, error) {
	// Get latest order_id from VeloDB
	var maxOrderID int
	err := s.velodb.QueryRow("SELECT COALESCE(MAX(order_id), 0) FROM fact_orders").Scan(&maxOrderID)
	if err != nil {
		return 0, fmt.Errorf("failed to get max order_id from VeloDB: %w", err)
	}

	// Query new orders from Postgres
	rows, err := s.postgres.Query(`
		SELECT order_id, partner_id, user_id, order_date, total_amount,
			   order_status, payment_method, created_at, updated_at
		FROM fact_orders
		WHERE order_id > $1
		ORDER BY order_id
		LIMIT 1000
	`, maxOrderID)
	if err != nil {
		return 0, fmt.Errorf("failed to query Postgres: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var orderID, partnerID, userID int
		var orderDate, createdAt, updatedAt time.Time
		var totalAmount float64
		var orderStatus string
		var paymentMethod sql.NullString

		if err := rows.Scan(&orderID, &partnerID, &userID, &orderDate, &totalAmount,
			&orderStatus, &paymentMethod, &createdAt, &updatedAt); err != nil {
			return count, fmt.Errorf("failed to scan row: %w", err)
		}

		paymentMethodStr := ""
		if paymentMethod.Valid {
			paymentMethodStr = paymentMethod.String
		}

		// Insert into VeloDB
		_, err := s.velodb.Exec(`
			INSERT INTO fact_orders (order_id, partner_id, user_id, order_date, total_amount,
			                         order_status, payment_method, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orderID, partnerID, userID, orderDate, totalAmount, orderStatus, paymentMethodStr, createdAt, updatedAt)
		if err != nil {
			log.Printf("[SyncService] Failed to insert order %d: %v", orderID, err)
			continue
		}
		count++
	}

	return count, nil
}

func (s *SyncService) syncOrderItems() (int, error) {
	// Get latest order_item_id from VeloDB
	var maxItemID int
	err := s.velodb.QueryRow("SELECT COALESCE(MAX(order_item_id), 0) FROM fact_order_items").Scan(&maxItemID)
	if err != nil {
		return 0, fmt.Errorf("failed to get max order_item_id from VeloDB: %w", err)
	}

	// Query new items from Postgres
	rows, err := s.postgres.Query(`
		SELECT order_item_id, order_id, partner_id, product_id, quantity,
			   unit_price, discount_percent, line_total, created_at
		FROM fact_order_items
		WHERE order_item_id > $1
		ORDER BY order_item_id
		LIMIT 1000
	`, maxItemID)
	if err != nil {
		return 0, fmt.Errorf("failed to query Postgres: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var orderItemID, orderID, partnerID, productID, quantity int
		var unitPrice, discountPercent, lineTotal float64
		var createdAt time.Time

		if err := rows.Scan(&orderItemID, &orderID, &partnerID, &productID, &quantity,
			&unitPrice, &discountPercent, &lineTotal, &createdAt); err != nil {
			return count, fmt.Errorf("failed to scan row: %w", err)
		}

		// Insert into VeloDB
		_, err := s.velodb.Exec(`
			INSERT INTO fact_order_items (order_item_id, order_id, partner_id, product_id,
			                              quantity, unit_price, discount_percent, line_total, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orderItemID, orderID, partnerID, productID, quantity, unitPrice, discountPercent, lineTotal, createdAt)
		if err != nil {
			log.Printf("[SyncService] Failed to insert order item %d: %v", orderItemID, err)
			continue
		}
		count++
	}

	return count, nil
}

func (s *SyncService) recordError(err error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.errors++
	s.lastError = err.Error()
}

func (s *SyncService) GetStatus() *SyncStatus {
	s.runningMu.RLock()
	running := s.running
	s.runningMu.RUnlock()

	s.syncMu.RLock()
	defer s.syncMu.RUnlock()

	return &SyncStatus{
		Running:      running,
		LastSyncTime: s.lastSyncTime,
		SyncedOrders: s.syncedOrders,
		SyncedItems:  s.syncedItems,
		Errors:       s.errors,
		LastError:    s.lastError,
	}
}

// TriggerSync manually triggers an immediate sync
func (s *SyncService) TriggerSync() {
	go s.performSync()
}
