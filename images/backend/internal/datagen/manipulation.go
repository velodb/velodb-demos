package datagen

import (
	"fmt"
	"math/rand"
	"time"
)

// VIPOrderGenerator generates VIP orders for demo manipulation
type VIPOrderGenerator struct {
	opts    *GeneratorOptions
	rand    *rand.Rand
	orderID int
}

// NewVIPOrderGenerator creates a new VIP order generator
func NewVIPOrderGenerator(opts *GeneratorOptions) *VIPOrderGenerator {
	return &VIPOrderGenerator{
		opts:    opts,
		rand:    opts.Rand,
		orderID: 900000, // Start from 900000 to avoid conflicts
	}
}

// Generate generates a VIP order designed for visible impact
func (g *VIPOrderGenerator) Generate() (string, error) {
	vipConfig := g.opts.Partner.VIPOrder

	// Calculate order amount based on partner tier
	orderAmount := RandomInRange(g.rand, vipConfig.MinAmount, vipConfig.MaxAmount)

	// Calculate expected hourly revenue
	hourlyRevenue := float64(g.opts.Partner.Volume.GMVDaily) / 24.0
	impactPercent := (orderAmount / hourlyRevenue) * 100

	// Pick 2-3 trending products
	numProducts := 2 + g.rand.Intn(2)
	products := make([]string, numProducts)
	totalItems := 0

	for i := 0; i < numProducts; i++ {
		// Pick products from top 20% of catalog (trending)
		trendingRange := g.opts.Partner.Volume.SKUCount / 5
		if trendingRange == 0 {
			trendingRange = 1
		}
		skuNum := 1 + g.rand.Intn(trendingRange)
		products[i] = fmt.Sprintf("SKU-%d-%05d", g.opts.Partner.ID, skuNum)
		totalItems += 5 + g.rand.Intn(10) // 5-15 items per product
	}

	// Generate order SQL
	orderTime := time.Now().Format("2006-01-02 15:04:05")
	userID := 1000 + g.rand.Intn(100) // VIP user

	var output string

	// Header with impact information
	output += fmt.Sprintf("-- VIP Order for Partner %d (%s)\n", g.opts.Partner.ID, g.opts.Partner.Name)
	output += fmt.Sprintf("-- Amount: $%.2f (%.1f%% of hourly revenue)\n", orderAmount, impactPercent)
	output += fmt.Sprintf("-- Products: %d trending items (%d total items)\n", numProducts, totalItems)
	output += fmt.Sprintf("-- Expected effects:\n")
	output += fmt.Sprintf("--   - Revenue spike visible in charts\n")
	output += fmt.Sprintf("--   - Product ranking shifts for trending products\n")
	output += fmt.Sprintf("--   - Real-time dashboard updates within 2 seconds\n")
	output += fmt.Sprintf("--\n")

	// Generate order INSERT
	output += fmt.Sprintf(
		"INSERT INTO fact_orders (partner_id, order_id, user_id, total_amount, order_date, status) VALUES (%d, %d, %d, %.2f, '%s', 'completed');\n",
		g.opts.Partner.ID,
		g.orderID,
		userID,
		orderAmount,
		orderTime,
	)

	// Generate order items
	itemAmount := orderAmount / float64(numProducts)
	for i, sku := range products {
		quantity := 5 + g.rand.Intn(10)
		pricePerUnit := itemAmount / float64(quantity)

		output += fmt.Sprintf(
			"INSERT INTO fact_order_items (partner_id, order_id, sku, quantity, price) VALUES (%d, %d, '%s', %d, %.2f);\n",
			g.opts.Partner.ID,
			g.orderID,
			sku,
			quantity,
			pricePerUnit,
		)

		// Add comment about expected ranking change
		if i == 0 {
			output += fmt.Sprintf("-- Product %s expected to rise in rankings\n", sku)
		}
	}

	return output, nil
}
