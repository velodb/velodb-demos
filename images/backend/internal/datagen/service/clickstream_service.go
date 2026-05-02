package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"velodb-demo/backend/internal/datagen"
)

type ClickstreamService struct {
	config   *ClickstreamConfig
	producer sarama.SyncProducer
	db       *sql.DB

	ctx    context.Context
	cancel context.CancelFunc

	status   Status
	statusMu sync.RWMutex

	buffer   [][]byte
	bufferMu sync.Mutex

	spikeTimer *time.Timer

	// Valid user ID pool for FK-safe generation
	validUserIDs []int
	idsMu        sync.RWMutex
}

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

// PartnerConversionRate defines per-partner funnel conversion rates
// These create differentiated metrics in the dashboard conversion funnel
type PartnerConversionRate struct {
	ViewWeight     int // Weight for view events
	CartWeight     int // Weight for add_to_cart events
	PurchaseWeight int // Weight for purchase events
	RemoveWeight   int // Weight for remove_from_cart events
}

// partnerConversionRates defines conversion patterns per partner
// Weights are calculated to produce realistic funnel ratios:
// - Views must be highest (top of funnel)
// - Carts = Views × view_to_cart_rate
// - Purchases = Carts × cart_to_purchase_rate
// TechMart: High-intent buyers (research-heavy, high purchase rate)
// StyleHub: Browsing shoppers (moderate conversion, impulse buys)
// LocalBoutique: Discovery mode (high cart rate, lower completion)
var partnerConversionRates = map[int]PartnerConversionRate{
	44: { // TechMart (Enterprise) - View->Cart 45%, Cart->Purchase 75%
		// 100 views → 45 carts → 34 purchases (high intent buyers)
		ViewWeight:     100,
		CartWeight:     45, // 45% of views
		PurchaseWeight: 34, // 75% of carts (0.45 * 0.75 = 0.34)
		RemoveWeight:   5,
	},
	45: { // StyleHub (Growth) - View->Cart 55%, Cart->Purchase 60%
		// 100 views → 55 carts → 33 purchases (fashion browsing)
		ViewWeight:     100,
		CartWeight:     55, // 55% of views
		PurchaseWeight: 33, // 60% of carts (0.55 * 0.60 = 0.33)
		RemoveWeight:   10,
	},
	46: { // LocalBoutique (Startup) - View->Cart 70%, Cart->Purchase 50%
		// 100 views → 70 carts → 35 purchases (discovery/gift mode)
		ViewWeight:     100,
		CartWeight:     70, // 70% of views (high engagement)
		PurchaseWeight: 35, // 50% of carts (0.70 * 0.50 = 0.35)
		RemoveWeight:   15, // more removes (gift research)
	},
}

// defaultConversionRate is the fallback for unknown partners
var defaultConversionRate = PartnerConversionRate{
	ViewWeight:     50,
	CartWeight:     30,
	PurchaseWeight: 15,
	RemoveWeight:   5,
}

func NewClickstreamService(config *ClickstreamConfig) (*ClickstreamService, error) {
	// Create Kafka producer with Redpanda-compatible settings
	kafkaConfig := sarama.NewConfig()
	kafkaConfig.Producer.Return.Successes = true
	kafkaConfig.Producer.RequiredAcks = sarama.WaitForAll
	kafkaConfig.Producer.Retry.Max = 3
	// Set Kafka version for Redpanda compatibility
	kafkaConfig.Version = sarama.V2_1_0_0
	// Network settings for local Docker
	kafkaConfig.Net.DialTimeout = 10 * time.Second
	kafkaConfig.Net.ReadTimeout = 10 * time.Second
	kafkaConfig.Net.WriteTimeout = 10 * time.Second
	kafkaConfig.Metadata.Retry.Max = 5
	kafkaConfig.Metadata.Retry.Backoff = 500 * time.Millisecond

	log.Printf("[ClickstreamService] Connecting to Kafka brokers: %v", config.KafkaBrokers)
	producer, err := sarama.NewSyncProducer(config.KafkaBrokers, kafkaConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kafka producer: %w", err)
	}

	return &ClickstreamService{
		config:   config,
		producer: producer,
		db:       config.DB,
		status: Status{
			BaselineRate: config.BaselineRate,
			CurrentRate:  config.BaselineRate,
		},
		buffer:       make([][]byte, 0, 1000),
		validUserIDs: make([]int, 0),
	}, nil
}

// LoadValidIDs generates synthetic user IDs for clickstream events
// Uses the same customer_id range as ecommerce orders (1-52) for data correlation
func (s *ClickstreamService) LoadValidIDs() error {
	s.idsMu.Lock()
	defer s.idsMu.Unlock()

	log.Printf("[ClickstreamService] Generating user IDs matching ecommerce customer_id range (1-52)")

	// Use customer IDs 1-52 to match ecommerce order generator
	// This ensures clickstream events can be correlated with orders by user_id/customer_id
	s.validUserIDs = make([]int, 52)
	for i := 0; i < 52; i++ {
		s.validUserIDs[i] = i + 1 // 1 to 52
	}

	log.Printf("[ClickstreamService] Generated %d user IDs (1-52) matching ecommerce customer_id range",
		len(s.validUserIDs))

	return nil
}

func (s *ClickstreamService) Start(ctx context.Context) error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if s.status.Running {
		return fmt.Errorf("service already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.status.Running = true
	s.status.StartedAt = time.Now()

	// Start generation and flush goroutines
	go s.generateLoop()
	go s.flushLoop()

	log.Printf("[ClickstreamService] Started with rate %d events/sec for partner %d",
		s.config.BaselineRate, s.config.PartnerID)

	return nil
}

func (s *ClickstreamService) generateLoop() {
	// Calculate interval between events
	rate := s.getCurrentRate()
	interval := time.Second / time.Duration(rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			s.flushBuffer() // Final flush
			log.Println("[ClickstreamService] Stopped")
			return
		case <-ticker.C:
			if err := s.generateOne(); err != nil {
				log.Printf("[ClickstreamService] Generation error: %v", err)
				s.incrementErrors()
				continue
			}
			s.incrementCount()

			// Update ticker if rate changed
			newRate := s.getCurrentRate()
			if newRate != rate {
				rate = newRate
				interval = time.Second / time.Duration(rate)
				ticker.Reset(interval)
			}
		}
	}
}

func (s *ClickstreamService) generateOne() error {
	// Select partner using weighted distribution (70% TechMart, 20% StyleHub, 10% LocalBoutique)
	partnerID := s.selectPartner()

	// Generate user ID using partner-specific ranges (matches ecommerce customer_id)
	// Partner 44: 1-10000, Partner 45: 10001-12000, Partner 46: 12001-12500
	userID := datagen.GetUserIDForPartner(partnerID)

	// Generate a realistic clickstream event
	eventID := fmt.Sprintf("evt_%d_%d", partnerID, time.Now().UnixNano())
	sessionID := fmt.Sprintf("sess_%d_%d", partnerID, rand.Intn(10000))

	// Get partner-specific conversion rates
	conversionRate, ok := partnerConversionRates[partnerID]
	if !ok {
		conversionRate = defaultConversionRate
	}

	// Build weighted event types based on partner conversion rates
	eventType := s.selectEventType(conversionRate)

	// Device type distribution: 70% mobile, 25% desktop, 5% tablet
	deviceTypes := []string{
		"mobile", "mobile", "mobile", "mobile", "mobile", "mobile", "mobile",
		"desktop", "desktop",
		"tablet",
	}
	deviceType := deviceTypes[rand.Intn(len(deviceTypes))]

	// OS distribution
	var deviceOS string
	if deviceType == "mobile" {
		deviceOS = []string{"Android", "iOS"}[rand.Intn(2)]
	} else {
		deviceOS = []string{"Windows", "MacOS", "Linux"}[rand.Intn(3)]
	}

	// Pick a random product from the partner's catalog
	// This ensures clickstream products match the ecommerce products
	product := datagen.GetRandomProductForPartner(partnerID)

	event := ClickstreamEvent{
		EventID:        eventID,
		SessionID:      sessionID,
		PartnerID:      partnerID,
		UserID:         userID,
		EventType:      eventType,
		EventTimestamp: time.Now().Format("2006-01-02 15:04:05"),
		PageURL:        fmt.Sprintf("/products/%s", product.Name),
		DeviceInfo: map[string]string{
			"type": deviceType,
			"os":   deviceOS,
		},
		EventProperties: map[string]interface{}{
			"product_name": product.Name,
			"category":     product.Category,
			"price":        product.Price,
			"referrer":     "google.com",
		},
		UTMParams: map[string]string{
			"source":   []string{"google", "facebook", "direct"}[rand.Intn(3)],
			"medium":   "cpc",
			"campaign": "summer_sale",
		},
	}

	// Serialize to JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Add to buffer
	s.addToBuffer(eventJSON)

	return nil
}

func (s *ClickstreamService) addToBuffer(eventJSON []byte) {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()

	s.buffer = append(s.buffer, eventJSON)

	// Flush if buffer is full (100 events for faster real-time demo)
	if len(s.buffer) >= 100 {
		go s.flushBuffer()
	}
}

func (s *ClickstreamService) flushLoop() {
	ticker := time.NewTicker(1 * time.Second) // Faster flush for real-time demo
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.flushBuffer()
		}
	}
}

func (s *ClickstreamService) flushBuffer() {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()

	if len(s.buffer) == 0 {
		return
	}

	// Prepare Kafka messages
	messages := make([]*sarama.ProducerMessage, len(s.buffer))
	for i, eventJSON := range s.buffer {
		messages[i] = &sarama.ProducerMessage{
			Topic: s.config.KafkaTopic,
			Value: sarama.ByteEncoder(eventJSON),
		}
	}

	// Send batch
	err := s.producer.SendMessages(messages)
	if err != nil {
		log.Printf("[ClickstreamService] Failed to send batch: %v", err)
		s.incrementErrors()
		return
	}

	log.Printf("[ClickstreamService] Flushed %d events to Kafka", len(s.buffer))

	// Clear buffer
	s.buffer = s.buffer[:0]
}

func (s *ClickstreamService) EnableSpike(multiplier int, duration time.Duration) error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if !s.status.Running {
		return fmt.Errorf("service not running")
	}

	s.status.SpikeActive = true
	s.status.SpikeMultiplier = multiplier
	s.status.CurrentRate = s.status.BaselineRate * multiplier
	s.status.SpikeEndsAt = time.Now().Add(duration)

	log.Printf("[ClickstreamService] Spike enabled: %dx rate for %v (new rate: %d/sec)",
		multiplier, duration, s.status.CurrentRate)

	if s.spikeTimer != nil {
		s.spikeTimer.Stop()
	}
	s.spikeTimer = time.AfterFunc(duration, func() {
		s.DisableSpike()
	})

	return nil
}

func (s *ClickstreamService) DisableSpike() error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	s.status.SpikeActive = false
	s.status.SpikeMultiplier = 0
	s.status.CurrentRate = s.status.BaselineRate
	s.status.SpikeEndsAt = time.Time{}

	log.Printf("[ClickstreamService] Spike disabled, back to baseline: %d/sec", s.status.BaselineRate)

	return nil
}

func (s *ClickstreamService) SetRate(rate int) error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	s.status.BaselineRate = rate
	if !s.status.SpikeActive {
		s.status.CurrentRate = rate
	}

	log.Printf("[ClickstreamService] Baseline rate updated to %d/sec", rate)

	return nil
}

func (s *ClickstreamService) GetStatus() *Status {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	statusCopy := s.status
	return &statusCopy
}

func (s *ClickstreamService) Stop() error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if !s.status.Running {
		return nil
	}

	s.cancel()
	s.status.Running = false

	if s.spikeTimer != nil {
		s.spikeTimer.Stop()
	}

	// Close producer
	if s.producer != nil {
		s.producer.Close()
	}

	log.Println("[ClickstreamService] Stopping...")

	return nil
}

func (s *ClickstreamService) getCurrentRate() int {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.status.CurrentRate
}

func (s *ClickstreamService) incrementCount() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.EventsGenerated++
}

func (s *ClickstreamService) incrementErrors() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.Errors++
}

// Reset resets the service metrics and event counters
func (s *ClickstreamService) Reset() error {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	// Reset metrics
	s.status.EventsGenerated = 0
	s.status.Errors = 0

	log.Printf("[ClickstreamService] Service reset complete")
	return nil
}

// selectEventType returns an event type based on partner-specific conversion weights
// This creates differentiated conversion funnels per partner in the dashboard
func (s *ClickstreamService) selectEventType(rate PartnerConversionRate) string {
	total := rate.ViewWeight + rate.CartWeight + rate.PurchaseWeight + rate.RemoveWeight
	pick := rand.Intn(total)

	if pick < rate.ViewWeight {
		return "view"
	}
	pick -= rate.ViewWeight

	if pick < rate.CartWeight {
		return "cart"
	}
	pick -= rate.CartWeight

	if pick < rate.PurchaseWeight {
		return "purchase"
	}

	return "remove"
}

// selectPartner returns a partner ID based on weighted distribution
// TechMart (44): 70%, StyleHub (45): 20%, LocalBoutique (46): 10%
// This matches the ecommerce order distribution for consistent metrics
func (s *ClickstreamService) selectPartner() int {
	pick := rand.Intn(100)
	if pick < 70 {
		return 44 // TechMart (Enterprise) - 70%
	} else if pick < 90 {
		return 45 // StyleHub (Growth) - 20%
	}
	return 46 // LocalBoutique (Startup) - 10%
}
