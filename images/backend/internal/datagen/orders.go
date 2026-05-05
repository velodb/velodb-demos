package datagen

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

// Order represents an order
type Order struct {
	PartnerID  int       `json:"partner_id"`
	OrderID    int       `json:"order_id"`
	UserID     int       `json:"user_id"`
	TotalAmount float64  `json:"total_amount"`
	OrderDate   string   `json:"order_date"`
	Status      string   `json:"status"`
}

// OrderItem represents an item in an order
type OrderItem struct {
	PartnerID int     `json:"partner_id"`
	OrderID   int     `json:"order_id"`
	ProductID int     `json:"product_id"` // Product ID for FK relationship
	SKU       string  `json:"sku"`
	Quantity  int     `json:"quantity"`
	UnitPrice float64 `json:"unit_price"` // Renamed from Price
	LineTotal float64 `json:"line_total"` // Calculated: quantity × unit_price
}

// OrderGenerator generates historical orders
type OrderGenerator struct {
	opts       *GeneratorOptions
	orderID    int
	rand       *rand.Rand
	startDate  time.Time
	endDate    time.Time
}

// NewOrderGenerator creates a new order generator
func NewOrderGenerator(opts *GeneratorOptions) *OrderGenerator {
	now := time.Now()
	startDate := now.AddDate(0, 0, -opts.Days)

	// Partition order ID ranges by partner to avoid conflicts
	// Each partner gets 400K ID space for future growth
	var startOrderID int
	switch opts.Partner.ID {
	case 44:
		startOrderID = 100000 // Partner 44: 100,000 - 499,999
	case 45:
		startOrderID = 500000 // Partner 45: 500,000 - 899,999
	case 46:
		startOrderID = 900000 // Partner 46: 900,000 - 1,299,999
	default:
		startOrderID = 100000 + (opts.Partner.ID * 400000)
	}

	return &OrderGenerator{
		opts:      opts,
		orderID:   startOrderID,
		rand:      opts.Rand,
		startDate: startDate,
		endDate:   now,
	}
}

// Generate generates historical order data
func (g *OrderGenerator) Generate() (string, error) {
	var output strings.Builder

	switch g.opts.Output {
	case "postgres":
		output.WriteString("-- Orders and Order Items for Partner " + fmt.Sprintf("%d", g.opts.Partner.ID) + "\n")
		output.WriteString("BEGIN;\n\n")

		// Generate orders for each day
		for day := 0; day < g.opts.Days; day++ {
			currentDate := g.startDate.AddDate(0, 0, day)
			dailyOrders := g.calculateDailyVolume(day)

			for i := 0; i < dailyOrders; i++ {
				order, items := g.generateOrder(currentDate)

				// Write order
				output.WriteString(g.formatOrderPostgres(order))
				output.WriteString("\n")

				// Write order items
				for _, item := range items {
					output.WriteString(g.formatOrderItemPostgres(item))
					output.WriteString("\n")
				}
			}
		}

		output.WriteString("\nCOMMIT;\n")

	case "json":
		for day := 0; day < g.opts.Days; day++ {
			currentDate := g.startDate.AddDate(0, 0, day)
			dailyOrders := g.calculateDailyVolume(day)

			for i := 0; i < dailyOrders; i++ {
				order, items := g.generateOrder(currentDate)

				result := map[string]interface{}{
					"order": order,
					"items": items,
				}

				data, _ := json.Marshal(result)
				output.Write(data)
				output.WriteString("\n")
			}
		}

	default:
		return "", fmt.Errorf("unsupported output format: %s", g.opts.Output)
	}

	return output.String(), nil
}

func (g *OrderGenerator) calculateDailyVolume(dayIndex int) int {
	baseVolume := float64(g.opts.Partner.Volume.OrdersPerDay)

	switch g.opts.Partner.Growth.Type {
	case "steady":
		// No growth, just add some volatility
		volatility := g.opts.Partner.Growth.Volatility
		variation := 1.0 + (g.rand.Float64()*2-1)*volatility
		return int(baseVolume * variation)

	case "growing":
		// 5% week-over-week growth
		weeklyGrowth := g.opts.Partner.Growth.Rate
		weeksElapsed := float64(dayIndex) / 7.0
		growthFactor := math.Pow(1.0+weeklyGrowth, weeksElapsed)

		volatility := g.opts.Partner.Growth.Volatility
		variation := 1.0 + (g.rand.Float64()*2-1)*volatility

		return int(baseVolume * growthFactor * variation)

	case "volatile":
		// High volatility, no sustained growth
		volatility := g.opts.Partner.Growth.Volatility
		variation := 1.0 + (g.rand.Float64()*2-1)*volatility
		return int(baseVolume * variation)

	default:
		return int(baseVolume)
	}
}

func (g *OrderGenerator) generateOrder(date time.Time) (*Order, []*OrderItem) {
	// Apply time pattern to order timestamp
	orderTime := ApplyTimePattern(g.rand, date, g.opts.Global.TimePatterns)

	// Pick a random user segment
	segment := PickWeighted(g.rand, g.opts.Partner.CustomerSegments)

	// Generate order value based on segment
	var orderValue float64
	switch segment {
	case "vip":
		orderValue = RandomInRange(g.rand, 100.0, 500.0)
	case "standard":
		orderValue = RandomInRange(g.rand, 30.0, 150.0)
	case "trial":
		orderValue = RandomInRange(g.rand, 10.0, 50.0)
	default:
		orderValue = RandomInRange(g.rand, 20.0, 100.0)
	}

	// Generate a random user ID from partner-specific range
	// Partner ID ranges: 44: 1K-50K, 45: 51K-100K, 46: 101K-150K
	var startUserID int
	switch g.opts.Partner.ID {
	case 44:
		startUserID = 1000
	case 45:
		startUserID = 51000
	case 46:
		startUserID = 101000
	default:
		startUserID = 1000 + (g.opts.Partner.ID * 50000)
	}

	numUsers := (g.opts.Partner.Volume.OrdersPerDay * 30) / 10 // Avg 10 orders per user over 30 days
	userID := startUserID + g.rand.Intn(numUsers)

	order := &Order{
		PartnerID:   g.opts.Partner.ID,
		OrderID:     g.orderID,
		UserID:      userID,
		TotalAmount: orderValue,
		OrderDate:   orderTime.Format("2006-01-02 15:04:05"),
		Status:      "completed",
	}

	// Generate 1-5 order items
	numItems := 1 + g.rand.Intn(5)
	items := make([]*OrderItem, numItems)
	itemValue := orderValue / float64(numItems)

	for i := 0; i < numItems; i++ {
		skuNum := 1 + g.rand.Intn(g.opts.Partner.Volume.SKUCount)
		sku := fmt.Sprintf("SKU-%d-%05d", g.opts.Partner.ID, skuNum)

		// Calculate product_id based on partner offset
		// Products are inserted with SERIAL IDs: Partner 44: 1-1000, 45: 1001-1200, 46: 1201-1250
		var productIDOffset int
		switch g.opts.Partner.ID {
		case 44:
			productIDOffset = 0 // Products 1-1000
		case 45:
			productIDOffset = 1000 // Products 1001-1200
		case 46:
			productIDOffset = 1200 // Products 1201-1250
		default:
			productIDOffset = 0
		}
		productID := productIDOffset + skuNum

		quantity := 1 + g.rand.Intn(3)
		unitPrice := itemValue
		lineTotal := float64(quantity) * unitPrice

		items[i] = &OrderItem{
			PartnerID: g.opts.Partner.ID,
			OrderID:   g.orderID,
			ProductID: productID,
			SKU:       sku,
			Quantity:  quantity,
			UnitPrice: unitPrice,
			LineTotal: lineTotal,
		}
	}

	g.orderID++
	return order, items
}

func (g *OrderGenerator) formatOrderPostgres(order *Order) string {
	return fmt.Sprintf(
		"INSERT INTO fact_orders (order_id, partner_id, user_id, total_amount, order_date, order_status) VALUES (%d, %d, %d, %.2f, '%s', '%s');",
		order.OrderID,
		order.PartnerID,
		order.UserID,
		order.TotalAmount,
		order.OrderDate,
		order.Status,
	)
}

func (g *OrderGenerator) formatOrderItemPostgres(item *OrderItem) string {
	return fmt.Sprintf(
		"INSERT INTO fact_order_items (partner_id, order_id, product_id, quantity, unit_price, line_total) VALUES (%d, %d, %d, %d, %.2f, %.2f);",
		item.PartnerID,
		item.OrderID,
		item.ProductID,
		item.Quantity,
		item.UnitPrice,
		item.LineTotal,
	)
}
