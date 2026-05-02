package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
)

// ClickstreamSyncService consumes clickstream events from Kafka and loads them to VeloDB via SQL
type ClickstreamSyncService struct {
	kafkaBrokers []string
	kafkaTopic   string
	velodb       *sql.DB

	consumer sarama.ConsumerGroup

	ctx    context.Context
	cancel context.CancelFunc

	running   bool
	runningMu sync.RWMutex

	eventsConsumed int64
	eventsLoaded   int64
	errors         int64
	lastError      string
	syncMu         sync.RWMutex

	buffer   [][]byte
	bufferMu sync.Mutex
}

type ClickstreamSyncConfig struct {
	KafkaBrokers []string
	KafkaTopic   string
	VeloDB       *sql.DB
}

type ClickstreamSyncStatus struct {
	Running        bool   `json:"running"`
	EventsConsumed int64  `json:"events_consumed"`
	EventsLoaded   int64  `json:"events_loaded"`
	Errors         int64  `json:"errors"`
	LastError      string `json:"last_error,omitempty"`
}

// ClickstreamEvent mirrors the Kafka message structure
type ClickstreamEvent struct {
	EventID         string                 `json:"event_id"`
	SessionID       string                 `json:"session_id"`
	PartnerID       int                    `json:"partner_id"`
	UserID          int                    `json:"user_id"`
	EventType       string                 `json:"event_type"`
	EventTimestamp  string                 `json:"event_timestamp"`
	PageURL         string                 `json:"page_url"`
	DeviceInfo      map[string]string      `json:"device_info"`
	EventProperties map[string]interface{} `json:"event_properties"`
	UTMParams       map[string]string      `json:"utm_params"`
}

func NewClickstreamSyncService(cfg *ClickstreamSyncConfig) (*ClickstreamSyncService, error) {
	return &ClickstreamSyncService{
		kafkaBrokers: cfg.KafkaBrokers,
		kafkaTopic:   cfg.KafkaTopic,
		velodb:       cfg.VeloDB,
		buffer:       make([][]byte, 0, 1000),
	}, nil
}

func (s *ClickstreamSyncService) Start(ctx context.Context) error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if s.running {
		return fmt.Errorf("clickstream sync service already running")
	}

	// Create consumer group config
	config := sarama.NewConfig()
	config.Version = sarama.V2_1_0_0
	config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	config.Consumer.Offsets.Initial = sarama.OffsetNewest // Start from newest to avoid backlog
	config.Net.DialTimeout = 10 * time.Second
	config.Net.ReadTimeout = 10 * time.Second
	config.Net.WriteTimeout = 10 * time.Second

	consumer, err := sarama.NewConsumerGroup(s.kafkaBrokers, "velodb-clickstream-sync", config)
	if err != nil {
		return fmt.Errorf("failed to create consumer group: %w", err)
	}
	s.consumer = consumer

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true

	go s.consumeLoop()
	go s.flushLoop()

	log.Printf("[ClickstreamSync] Started consuming from %s", s.kafkaTopic)
	return nil
}

func (s *ClickstreamSyncService) consumeLoop() {
	handler := &clickstreamConsumerHandler{sync: s}

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			if err := s.consumer.Consume(s.ctx, []string{s.kafkaTopic}, handler); err != nil {
				log.Printf("[ClickstreamSync] Consumer error: %v", err)
				s.recordError(err)
				time.Sleep(5 * time.Second)
			}
		}
	}
}

func (s *ClickstreamSyncService) flushLoop() {
	ticker := time.NewTicker(500 * time.Millisecond) // Fast flush for real-time demo
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			s.flushBuffer() // Final flush
			return
		case <-ticker.C:
			s.flushBuffer()
		}
	}
}

func (s *ClickstreamSyncService) addToBuffer(msg []byte) {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()

	s.buffer = append(s.buffer, msg)

	s.syncMu.Lock()
	s.eventsConsumed++
	s.syncMu.Unlock()

	// Flush if buffer is full (smaller batch for faster real-time sync)
	if len(s.buffer) >= 50 {
		go s.flushBuffer()
	}
}

func (s *ClickstreamSyncService) flushBuffer() {
	s.bufferMu.Lock()
	if len(s.buffer) == 0 {
		s.bufferMu.Unlock()
		return
	}

	// Copy buffer and clear
	toFlush := make([][]byte, len(s.buffer))
	copy(toFlush, s.buffer)
	s.buffer = s.buffer[:0]
	s.bufferMu.Unlock()

	// Parse events and batch insert via SQL
	var events []ClickstreamEvent
	for _, msg := range toFlush {
		var event ClickstreamEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Printf("[ClickstreamSync] Failed to parse event: %v", err)
			continue
		}
		events = append(events, event)
	}

	if len(events) == 0 {
		return
	}

	// Build batch INSERT statement matching VeloDB fact_clickstream schema
	// VeloDB schema: event_id, event_time, partner_id, session_id, user_id, event_type,
	//               page_url, product_id, product_name, product_category, product_price,
	//               referrer, user_agent, device_type, country, city
	var values []string
	var args []interface{}

	for _, e := range events {
		// Extract product fields from EventProperties
		productName := ""
		productCategory := ""
		productPrice := 0.0
		referrer := ""

		if name, ok := e.EventProperties["product_name"].(string); ok {
			productName = name
		}
		if cat, ok := e.EventProperties["category"].(string); ok {
			productCategory = cat
		}
		if price, ok := e.EventProperties["price"].(float64); ok {
			productPrice = price
		}
		if ref, ok := e.EventProperties["referrer"].(string); ok {
			referrer = ref
		}

		// Extract device info
		deviceType := ""
		userAgent := ""
		if dt, ok := e.DeviceInfo["type"]; ok {
			deviceType = dt
		}
		if os, ok := e.DeviceInfo["os"]; ok {
			userAgent = os // Use OS as user_agent
		}

		values = append(values, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			e.EventID,
			e.EventTimestamp, // Maps to event_time
			e.PartnerID,
			e.SessionID,
			e.UserID,
			e.EventType,
			e.PageURL,
			productName,
			productCategory,
			productPrice,
			referrer,
			userAgent,
			deviceType,
			"", // country (not available in Kafka event)
		)
	}

	query := fmt.Sprintf(`
		INSERT INTO fact_clickstream
			(event_id, event_time, partner_id, session_id, user_id, event_type, page_url, product_name, product_category, product_price, referrer, user_agent, device_type, country)
		VALUES %s
	`, strings.Join(values, ", "))

	_, err := s.velodb.Exec(query, args...)
	if err != nil {
		log.Printf("[ClickstreamSync] Batch insert failed: %v", err)
		s.recordError(err)
		return
	}

	s.syncMu.Lock()
	s.eventsLoaded += int64(len(events))
	s.syncMu.Unlock()

	log.Printf("[ClickstreamSync] Loaded %d events to VeloDB (total: %d)", len(events), s.eventsLoaded)
}

func (s *ClickstreamSyncService) recordError(err error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.errors++
	s.lastError = err.Error()
}

func (s *ClickstreamSyncService) Stop() error {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return nil
	}

	s.cancel()
	s.running = false

	if s.consumer != nil {
		s.consumer.Close()
	}

	log.Println("[ClickstreamSync] Stopped")
	return nil
}

func (s *ClickstreamSyncService) GetStatus() *ClickstreamSyncStatus {
	s.runningMu.RLock()
	running := s.running
	s.runningMu.RUnlock()

	s.syncMu.RLock()
	defer s.syncMu.RUnlock()

	return &ClickstreamSyncStatus{
		Running:        running,
		EventsConsumed: s.eventsConsumed,
		EventsLoaded:   s.eventsLoaded,
		Errors:         s.errors,
		LastError:      s.lastError,
	}
}

// Consumer group handler
type clickstreamConsumerHandler struct {
	sync *ClickstreamSyncService
}

func (h *clickstreamConsumerHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *clickstreamConsumerHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *clickstreamConsumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			h.sync.addToBuffer(msg.Value)
			session.MarkMessage(msg, "")
		case <-session.Context().Done():
			return nil
		}
	}
}
