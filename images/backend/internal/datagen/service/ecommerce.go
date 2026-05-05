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

// EcommerceConfig holds configuration for ecommerce order generation
type EcommerceConfig struct {
	Enabled      bool
	BaselineRate int // Orders per minute
	DB           *sql.DB
	KafkaBrokers []string
	KafkaTopic   string
}

// EcommerceService generates Kibana-compatible ecommerce orders
type EcommerceService struct {
	config    *EcommerceConfig
	generator *datagen.EcommerceGenerator
	producer  sarama.SyncProducer

	ctx    context.Context
	cancel context.CancelFunc

	running      bool
	spikeEnabled bool
	spikeEndTime time.Time
	currentRate  int

	// Metrics
	generated int64
	errors    int64

	mu sync.RWMutex
}

// NewEcommerceService creates a new ecommerce service
func NewEcommerceService(config *EcommerceConfig) (*EcommerceService, error) {
	generator := datagen.NewEcommerceGenerator(0) // Random seed

	// Query max order_id from database to avoid conflicts
	if config.DB != nil {
		var maxOrderID int
		err := config.DB.QueryRow(`SELECT COALESCE(MAX(order_id), 0) FROM kibana_sample_data_ecommerce`).Scan(&maxOrderID)
		if err == nil && maxOrderID > 0 {
			generator.SetOrderID(maxOrderID + 1)
			log.Printf("[EcommerceService] Starting order ID set to %d (max in DB: %d)", maxOrderID+1, maxOrderID)
		}
	}

	svc := &EcommerceService{
		config:      config,
		generator:   generator,
		currentRate: config.BaselineRate,
	}

	// Create Kafka producer if brokers are configured
	if len(config.KafkaBrokers) > 0 && config.KafkaTopic != "" {
		kafkaConfig := sarama.NewConfig()
		kafkaConfig.Producer.Return.Successes = true
		kafkaConfig.Producer.RequiredAcks = sarama.WaitForAll
		kafkaConfig.Producer.Retry.Max = 3
		kafkaConfig.Version = sarama.V2_1_0_0
		kafkaConfig.Net.DialTimeout = 10 * time.Second
		kafkaConfig.Net.ReadTimeout = 10 * time.Second
		kafkaConfig.Net.WriteTimeout = 10 * time.Second
		kafkaConfig.Metadata.Retry.Max = 5
		kafkaConfig.Metadata.Retry.Backoff = 500 * time.Millisecond

		log.Printf("[EcommerceService] Connecting to Kafka brokers: %v, topic: %s", config.KafkaBrokers, config.KafkaTopic)
		producer, err := sarama.NewSyncProducer(config.KafkaBrokers, kafkaConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Kafka producer: %w", err)
		}
		svc.producer = producer
		log.Printf("[EcommerceService] Kafka producer created successfully")
	}

	return svc, nil
}

// Start begins generating ecommerce orders
func (s *EcommerceService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true

	go s.generateLoop()

	log.Printf("[EcommerceService] Started (rate: %d orders/min)", s.currentRate)
	return nil
}

// Stop halts order generation
func (s *EcommerceService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	if s.cancel != nil {
		s.cancel()
	}
	s.running = false

	// Close Kafka producer if exists
	if s.producer != nil {
		if err := s.producer.Close(); err != nil {
			log.Printf("[EcommerceService] Failed to close Kafka producer: %v", err)
		}
	}

	log.Println("[EcommerceService] Stopped")
}

func (s *EcommerceService) generateLoop() {
	// Generate orders at the configured rate (orders per minute)
	// Convert to interval: if 10 orders/min, interval = 6 seconds
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			s.mu.RLock()
			rate := s.currentRate
			s.mu.RUnlock()

			if rate <= 0 {
				time.Sleep(time.Second)
				continue
			}

			// Calculate interval for this rate
			interval := time.Minute / time.Duration(rate)
			if interval < 100*time.Millisecond {
				interval = 100 * time.Millisecond
			}

			// Generate one order
			if err := s.generateOrder(); err != nil {
				log.Printf("[EcommerceService] Error generating order: %v", err)
				s.mu.Lock()
				s.errors++
				s.mu.Unlock()
			}

			// Add some jitter
			jitter := time.Duration(rand.Intn(int(interval / 4)))
			time.Sleep(interval + jitter)

			// Check spike status
			s.checkSpikeStatus()
		}
	}
}

// TimePattern defines hourly multipliers for revenue patterns
// Each partner has different peak hours reflecting their customer behavior
type TimePattern struct {
	// HourWeights is a 24-element array with weight multipliers for each hour (0-23)
	// A value of 1.0 is baseline, 2.0 doubles the probability, 0.5 halves it
	HourWeights [24]float64
	// WeekendMultiplier adjusts probability on Saturday/Sunday (1.0 = no change)
	WeekendMultiplier float64
}

// partnerTimePatterns defines when each partner type is most active
var partnerTimePatterns = map[int]*TimePattern{
	// TechMart (44): Working professionals - lunch break (12-2pm) and evening (7-10pm) peaks
	44: {
		HourWeights: [24]float64{
			0.3, 0.2, 0.1, 0.1, 0.2, 0.3, // 00:00-05:59 - late night (low)
			0.5, 0.7, 0.8, 0.9, 1.0, 1.5, // 06:00-11:59 - morning buildup, lunch peak starts
			1.8, 1.5, 1.0, 0.9, 0.8, 0.9, // 12:00-17:59 - lunch peak, afternoon dip
			1.0, 1.5, 1.8, 1.6, 1.2, 0.6, // 18:00-23:59 - evening peak (7-10pm = indices 19-22)
		},
		WeekendMultiplier: 0.6, // Less activity on weekends (professionals shop during work breaks)
	},
	// StyleHub (45): Fashion shoppers - evening browsing (6-11pm) and weekend peaks
	45: {
		HourWeights: [24]float64{
			0.2, 0.1, 0.1, 0.1, 0.1, 0.2, // 00:00-05:59 - late night (very low)
			0.3, 0.4, 0.5, 0.6, 0.8, 1.0, // 06:00-11:59 - morning buildup
			1.1, 1.0, 0.9, 0.8, 0.9, 1.2, // 12:00-17:59 - lunch, afternoon shopping
			1.5, 1.8, 2.0, 2.0, 1.8, 1.2, // 18:00-23:59 - evening peak (6-11pm)
		},
		WeekendMultiplier: 1.8, // Much higher activity on weekends (fashion shopping time)
	},
	// LocalBoutique (46): Gift buyers - steady with slight evening preference
	46: {
		HourWeights: [24]float64{
			0.4, 0.3, 0.2, 0.2, 0.3, 0.4, // 00:00-05:59 - late night
			0.6, 0.7, 0.8, 0.9, 1.0, 1.1, // 06:00-11:59 - morning
			1.1, 1.1, 1.0, 1.0, 1.0, 1.1, // 12:00-17:59 - steady afternoon
			1.2, 1.3, 1.3, 1.2, 1.0, 0.7, // 18:00-23:59 - slight evening preference
		},
		WeekendMultiplier: 1.3, // Moderate increase on weekends (gift shopping)
	},
}

// selectPartner randomly selects a partner based on weighted distribution
// adjusted by time-of-day and day-of-week patterns
// Base weights: TechMart (44): 70%, StyleHub (45): 20%, LocalBoutique (46): 10%
func (s *EcommerceService) selectPartner() int {
	now := time.Now()
	hour := now.Hour()
	weekday := now.Weekday()
	isWeekend := weekday == time.Saturday || weekday == time.Sunday

	// Base weights (sum to 100)
	baseWeights := map[int]float64{
		44: 70.0, // TechMart
		45: 20.0, // StyleHub
		46: 10.0, // LocalBoutique
	}

	// Apply time pattern adjustments
	adjustedWeights := make(map[int]float64)
	for partnerID, baseWeight := range baseWeights {
		pattern := partnerTimePatterns[partnerID]
		if pattern == nil {
			adjustedWeights[partnerID] = baseWeight
			continue
		}

		// Apply hour weight
		weight := baseWeight * pattern.HourWeights[hour]

		// Apply weekend multiplier
		if isWeekend {
			weight *= pattern.WeekendMultiplier
		}

		adjustedWeights[partnerID] = weight
	}

	// Calculate total and normalize
	var totalWeight float64
	for _, w := range adjustedWeights {
		totalWeight += w
	}

	// Random selection based on adjusted weights
	roll := rand.Float64() * totalWeight
	cumulative := 0.0

	for _, partnerID := range []int{44, 45, 46} {
		cumulative += adjustedWeights[partnerID]
		if roll < cumulative {
			return partnerID
		}
	}

	return 44 // Fallback to TechMart
}

func (s *EcommerceService) generateOrder() error {
	// Select partner based on weighted distribution
	partnerID := s.selectPartner()
	// Use partner-specific catalog for differentiated products
	order := s.generator.GenerateOrderForPartner(partnerID)
	orderDate := time.Now().UTC()

	// Convert to DB format
	dbOrder := &EcommerceOrderDB{
		OrderDate:           orderDate,
		OrderID:             int64(order.OrderID),
		PartnerID:           partnerID,
		CustomerID:          int64(order.CustomerID),
		CustomerFirstName:   order.CustomerFirstName,
		CustomerLastName:    order.CustomerLastName,
		CustomerFullName:    order.CustomerFullName,
		CustomerGender:      order.CustomerGender,
		CustomerPhone:       order.CustomerPhone,
		Email:               order.Email,
		User:                order.User,
		DayOfWeek:           order.DayOfWeek,
		DayOfWeekI:          order.DayOfWeekI,
		Currency:            order.Currency,
		TaxfulTotalPrice:    order.TaxfulTotalPrice,
		TaxlessTotalPrice:   order.TaxlessTotalPrice,
		TotalQuantity:       order.TotalQuantity,
		TotalUniqueProducts: order.TotalUniqueProducts,
		OrderStatus:         "pending",
		Type:                "order",
		Category:            order.Category,
		Manufacturer:        order.Manufacturer,
		SKU:                 order.SKU,
		Products:            convertProducts(order.Products),
		GeoIP:               convertGeoIP(order.GeoIP),
		Event:               order.Event,
	}

	if err := InsertEcommerceOrderToDB(s.config.DB, dbOrder); err != nil {
		return err
	}

	// Publish to Kafka if producer is configured
	if s.producer != nil {
		kafkaOrder := map[string]interface{}{
			"order_id":              order.OrderID,
			"order_date":            orderDate.Format(time.RFC3339),
			"customer_id":           order.CustomerID,
			"customer_full_name":    order.CustomerFullName,
			"customer_first_name":   order.CustomerFirstName,
			"customer_last_name":    order.CustomerLastName,
			"customer_gender":       order.CustomerGender,
			"customer_phone":        order.CustomerPhone,
			"email":                 order.Email,
			"user":                  order.User,
			"currency":              order.Currency,
			"taxful_total_price":    order.TaxfulTotalPrice,
			"taxless_total_price":   order.TaxlessTotalPrice,
			"total_quantity":        order.TotalQuantity,
			"total_unique_products": order.TotalUniqueProducts,
			"order_status":          "pending",
			"type":                  "order",
			"category":              order.Category,
			"manufacturer":          order.Manufacturer,
			"sku":                   order.SKU,
			"products":              convertProducts(order.Products),
			"geoip":                 convertGeoIP(order.GeoIP),
			"event":                 order.Event,
		}

		orderJSON, err := json.Marshal(kafkaOrder)
		if err != nil {
			log.Printf("[EcommerceService] Failed to marshal order for Kafka: %v", err)
		} else {
			msg := &sarama.ProducerMessage{
				Topic: s.config.KafkaTopic,
				Value: sarama.ByteEncoder(orderJSON),
			}
			_, _, err := s.producer.SendMessage(msg)
			if err != nil {
				log.Printf("[EcommerceService] Failed to send to Kafka: %v", err)
			}
		}
	}

	s.mu.Lock()
	s.generated++
	s.mu.Unlock()

	return nil
}

func (s *EcommerceService) checkSpikeStatus() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.spikeEnabled && time.Now().After(s.spikeEndTime) {
		s.spikeEnabled = false
		s.currentRate = s.config.BaselineRate
		log.Printf("[EcommerceService] Spike ended, rate restored to %d orders/min", s.currentRate)
	}
}

// EnableSpike temporarily increases the generation rate
func (s *EcommerceService) EnableSpike(multiplier int, duration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.spikeEnabled = true
	s.spikeEndTime = time.Now().Add(duration)
	s.currentRate = s.config.BaselineRate * multiplier

	log.Printf("[EcommerceService] Spike enabled: %dx for %v (rate: %d orders/min)", multiplier, duration, s.currentRate)
	return nil
}

// DisableSpike ends the spike early
func (s *EcommerceService) DisableSpike() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.spikeEnabled = false
	s.currentRate = s.config.BaselineRate

	log.Printf("[EcommerceService] Spike disabled, rate restored to %d orders/min", s.currentRate)
	return nil
}

// SetRate updates the baseline generation rate
func (s *EcommerceService) SetRate(rate int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.BaselineRate = rate
	if !s.spikeEnabled {
		s.currentRate = rate
	}

	log.Printf("[EcommerceService] Rate updated to %d orders/min", rate)
	return nil
}

// GetStatus returns current service status
func (s *EcommerceService) GetStatus() *Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &Status{
		Running:         s.running,
		CurrentRate:     s.currentRate,
		BaselineRate:    s.config.BaselineRate,
		SpikeActive:     s.spikeEnabled,
		SpikeEndsAt:     s.spikeEndTime,
		EventsGenerated: s.generated,
		Errors:          s.errors,
	}
}

// Reset resets the service metrics and order ID counter
func (s *EcommerceService) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Reset metrics
	s.generated = 0
	s.errors = 0

	// Reset order ID counter to 1
	s.generator.SetOrderID(1)
	log.Printf("[EcommerceService] Service reset complete - next order ID will be 1")

	return nil
}

// CreatedOrderResponse represents the response from CreateSingleOrder
type CreatedOrderResponse struct {
	OrderID          int64                    `json:"order_id"`
	OrderDate        time.Time                `json:"order_date"`
	CustomerFullName string                   `json:"customer_full_name"`
	TotalPrice       float64                  `json:"taxful_total_price"`
	Currency         string                   `json:"currency"`
	Products         []map[string]interface{} `json:"products"`
	GeoIP            map[string]interface{}   `json:"geoip"`
	Category         []string                 `json:"category"`
	OrderStatus      string                   `json:"order_status"`
}

// BatchOrderResponse contains batch creation results
type BatchOrderResponse struct {
	BatchID          string            `json:"batch_id"`
	OrdersCreated    int               `json:"orders_created"`
	TimeRange        map[string]string `json:"time_range"`
	PartnerBreakdown map[int]int       `json:"partner_breakdown"`
	DurationSeconds  float64           `json:"duration_seconds"`
}

// CreateSingleOrder generates a single order and returns its details
// Uses partner-specific product catalog for differentiated products/pricing
func (s *EcommerceService) CreateSingleOrder(partnerID int) (*CreatedOrderResponse, error) {
	// Default to partner 44 if not set
	if partnerID == 0 {
		partnerID = 44
	}

	// Use partner-specific order generation for differentiated products
	order := s.generator.GenerateOrderForPartner(partnerID)
	orderDate := time.Now().UTC()

	// Convert to DB format
	dbOrder := &EcommerceOrderDB{
		OrderDate:           orderDate,
		OrderID:             int64(order.OrderID),
		PartnerID:           partnerID,
		CustomerID:          int64(order.CustomerID),
		CustomerFirstName:   order.CustomerFirstName,
		CustomerLastName:    order.CustomerLastName,
		CustomerFullName:    order.CustomerFullName,
		CustomerGender:      order.CustomerGender,
		CustomerPhone:       order.CustomerPhone,
		Email:               order.Email,
		User:                order.User,
		DayOfWeek:           order.DayOfWeek,
		DayOfWeekI:          order.DayOfWeekI,
		Currency:            order.Currency,
		TaxfulTotalPrice:    order.TaxfulTotalPrice,
		TaxlessTotalPrice:   order.TaxlessTotalPrice,
		TotalQuantity:       order.TotalQuantity,
		TotalUniqueProducts: order.TotalUniqueProducts,
		OrderStatus:         "pending",
		Type:                "order",
		Category:            order.Category,
		Manufacturer:        order.Manufacturer,
		SKU:                 order.SKU,
		Products:            convertProducts(order.Products),
		GeoIP:               convertGeoIP(order.GeoIP),
		Event:               order.Event,
	}

	if err := InsertEcommerceOrderToDB(s.config.DB, dbOrder); err != nil {
		return nil, err
	}

	// Publish to Kafka if producer is configured
	if s.producer != nil {
		kafkaOrder := map[string]interface{}{
			"order_id":              order.OrderID,
			"order_date":            orderDate.Format(time.RFC3339),
			"customer_id":           order.CustomerID,
			"customer_full_name":    order.CustomerFullName,
			"customer_first_name":   order.CustomerFirstName,
			"customer_last_name":    order.CustomerLastName,
			"customer_gender":       order.CustomerGender,
			"customer_phone":        order.CustomerPhone,
			"email":                 order.Email,
			"user":                  order.User,
			"currency":              order.Currency,
			"taxful_total_price":    order.TaxfulTotalPrice,
			"taxless_total_price":   order.TaxlessTotalPrice,
			"total_quantity":        order.TotalQuantity,
			"total_unique_products": order.TotalUniqueProducts,
			"order_status":          "pending",
			"type":                  "order",
			"category":              order.Category,
			"manufacturer":          order.Manufacturer,
			"sku":                   order.SKU,
			"products":              convertProducts(order.Products),
			"geoip":                 convertGeoIP(order.GeoIP),
			"event":                 order.Event,
		}

		orderJSON, err := json.Marshal(kafkaOrder)
		if err != nil {
			log.Printf("[EcommerceService] Failed to marshal order for Kafka: %v", err)
		} else {
			msg := &sarama.ProducerMessage{
				Topic: s.config.KafkaTopic,
				Value: sarama.ByteEncoder(orderJSON),
			}
			_, _, err := s.producer.SendMessage(msg)
			if err != nil {
				log.Printf("[EcommerceService] Failed to send to Kafka: %v", err)
			}
		}
	}

	s.mu.Lock()
	s.generated++
	s.mu.Unlock()

	// Return the created order details
	return &CreatedOrderResponse{
		OrderID:          int64(order.OrderID),
		OrderDate:        orderDate,
		CustomerFullName: order.CustomerFullName,
		TotalPrice:       order.TaxfulTotalPrice,
		Currency:         order.Currency,
		Products:         convertProducts(order.Products),
		GeoIP:            convertGeoIP(order.GeoIP),
		Category:         order.Category,
		OrderStatus:      "pending",
	}, nil
}

// CreateBatchOrders generates multiple orders with historical timestamps
func (s *EcommerceService) CreateBatchOrders(count int, backdateDays int, partnerID int, distribution string) (*BatchOrderResponse, error) {
	startTime := time.Now()

	// Generate unique batch ID
	batchID := fmt.Sprintf("batch_%d", time.Now().Unix())

	// Partner distribution (70/20/10 for partners 44/45/46)
	partnerBreakdown := make(map[int]int)
	var earliest, latest time.Time

	now := time.Now().UTC()

	for i := 0; i < count; i++ {
		// Determine partner for this order
		orderPartnerID := partnerID
		if partnerID == 0 {
			// Use 70/20/10 distribution
			r := rand.Float64()
			if r < 0.70 {
				orderPartnerID = 44
			} else if r < 0.90 {
				orderPartnerID = 45
			} else {
				orderPartnerID = 46
			}
		}

		// Generate historical timestamp
		var orderDate time.Time
		if distribution == "uniform" {
			// Uniform distribution across backdate range
			randomSeconds := rand.Intn(backdateDays * 24 * 3600)
			orderDate = now.Add(-time.Duration(randomSeconds) * time.Second)
		} else {
			// Random distribution (default)
			randomSeconds := rand.Intn(backdateDays * 24 * 3600)
			orderDate = now.Add(-time.Duration(randomSeconds) * time.Second)
		}

		// Track earliest and latest
		if i == 0 {
			earliest = orderDate
			latest = orderDate
		} else {
			if orderDate.Before(earliest) {
				earliest = orderDate
			}
			if orderDate.After(latest) {
				latest = orderDate
			}
		}

		// Generate order for partner
		order := s.generator.GenerateOrderForPartner(orderPartnerID)

		// Convert to DB format
		dbOrder := &EcommerceOrderDB{
			OrderDate:           orderDate,
			OrderID:             int64(order.OrderID),
			PartnerID:           orderPartnerID,
			CustomerID:          int64(order.CustomerID),
			CustomerFirstName:   order.CustomerFirstName,
			CustomerLastName:    order.CustomerLastName,
			CustomerFullName:    order.CustomerFullName,
			CustomerGender:      order.CustomerGender,
			CustomerPhone:       order.CustomerPhone,
			Email:               order.Email,
			User:                order.User,
			DayOfWeek:           order.DayOfWeek,
			DayOfWeekI:          order.DayOfWeekI,
			Currency:            order.Currency,
			TaxfulTotalPrice:    order.TaxfulTotalPrice,
			TaxlessTotalPrice:   order.TaxlessTotalPrice,
			TotalQuantity:       order.TotalQuantity,
			TotalUniqueProducts: order.TotalUniqueProducts,
			OrderStatus:         "complete",
			Type:                "order",
			Category:            order.Category,
			Manufacturer:        order.Manufacturer,
			SKU:                 order.SKU,
			Products:            convertProducts(order.Products),
			GeoIP:               convertGeoIP(order.GeoIP),
			Event:               order.Event,
		}

		if err := InsertEcommerceOrderToDB(s.config.DB, dbOrder); err != nil {
			return nil, fmt.Errorf("failed to insert order %d: %w", i+1, err)
		}

		partnerBreakdown[orderPartnerID]++

		s.mu.Lock()
		s.generated++
		s.mu.Unlock()
	}

	duration := time.Since(startTime).Seconds()

	return &BatchOrderResponse{
		BatchID:       batchID,
		OrdersCreated: count,
		TimeRange: map[string]string{
			"earliest": earliest.Format(time.RFC3339),
			"latest":   latest.Format(time.RFC3339),
		},
		PartnerBreakdown: partnerBreakdown,
		DurationSeconds:  duration,
	}, nil
}

// EcommerceOrderDB represents an ecommerce order for database storage
type EcommerceOrderDB struct {
	OrderDate           time.Time
	OrderID             int64
	PartnerID           int
	CustomerID          int64
	CustomerFirstName   string
	CustomerLastName    string
	CustomerFullName    string
	CustomerGender      string
	CustomerPhone       string
	Email               string
	User                string
	DayOfWeek           string
	DayOfWeekI          int
	Currency            string
	TaxfulTotalPrice    float64
	TaxlessTotalPrice   float64
	TotalQuantity       int
	TotalUniqueProducts int
	OrderStatus         string
	Type                string
	Category            []string
	Manufacturer        []string
	SKU                 []string
	Products            []map[string]interface{}
	GeoIP               map[string]interface{}
	Event               map[string]string
}

// InsertEcommerceOrderToDB inserts an ecommerce order into Postgres
func InsertEcommerceOrderToDB(db *sql.DB, order *EcommerceOrderDB) error {
	categoryJSON, _ := json.Marshal(order.Category)
	manufacturerJSON, _ := json.Marshal(order.Manufacturer)
	skuJSON, _ := json.Marshal(order.SKU)
	productsJSON, _ := json.Marshal(order.Products)
	geoipJSON, _ := json.Marshal(order.GeoIP)
	eventJSON, _ := json.Marshal(order.Event)

	// Default to partner 44 if not set
	partnerID := order.PartnerID
	if partnerID == 0 {
		partnerID = 44
	}

	_, err := db.Exec(`
		INSERT INTO kibana_sample_data_ecommerce
			(order_date, order_id, partner_id, customer_id, customer_first_name, customer_last_name,
			 customer_full_name, customer_gender, customer_phone, email, "user",
			 day_of_week, day_of_week_i, currency,
			 taxful_total_price, taxless_total_price, total_quantity, total_unique_products,
			 order_status, type, category, manufacturer, sku, products, geoip, event)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
	`,
		order.OrderDate, order.OrderID, partnerID, order.CustomerID,
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

// Helper functions to convert datagen types to DB format
func convertProducts(products []datagen.EcommerceProduct) []map[string]interface{} {
	result := make([]map[string]interface{}, len(products))
	for i, p := range products {
		result[i] = map[string]interface{}{
			"_id":                  p.ID,
			"base_price":           p.BasePrice,
			"base_unit_price":      p.BaseUnitPrice,
			"category":             p.Category,
			"created_on":           p.CreatedOn,
			"discount_amount":      p.DiscountAmount,
			"discount_percentage":  p.DiscountPercentage,
			"manufacturer":         p.Manufacturer,
			"min_price":            p.MinPrice,
			"price":                p.Price,
			"product_id":           p.ProductID,
			"product_name":         p.ProductName,
			"quantity":             p.Quantity,
			"sku":                  p.SKU,
			"tax_amount":           p.TaxAmount,
			"taxful_price":         p.TaxfulPrice,
			"taxless_price":        p.TaxlessPrice,
			"unit_discount_amount": p.UnitDiscountAmount,
		}
	}
	return result
}

func convertGeoIP(geoip datagen.GeoIPInfo) map[string]interface{} {
	return map[string]interface{}{
		"city_name":        geoip.CityName,
		"continent_name":   geoip.ContinentName,
		"country_iso_code": geoip.CountryISOCode,
		"region_name":      geoip.RegionName,
		"location": map[string]interface{}{
			"lat": geoip.Location.Lat,
			"lon": geoip.Location.Lon,
		},
	}
}
