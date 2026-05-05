package db

import (
	"database/sql"
	"fmt"
	"log"
)

// SeedUsers creates test users for a given partner
// Returns the list of created user IDs
func SeedUsers(db *sql.DB, partnerID int, count int) ([]int, error) {
	if count <= 0 {
		return []int{}, nil
	}

	// Start user IDs from partnerID * 1000 to match generator logic
	baseUserID := partnerID * 1000
	userIDs := make([]int, 0, count)

	log.Printf("[Seed] Creating %d users for partner %d (user_id range: %d-%d)",
		count, partnerID, baseUserID, baseUserID+count-1)

	for i := 0; i < count; i++ {
		userID := baseUserID + i
		username := fmt.Sprintf("user_%d_%d", partnerID, i)
		email := fmt.Sprintf("user%d.p%d@test.com", i, partnerID)
		fullName := fmt.Sprintf("Test User %d", userID)

		_, err := db.Exec(`
			INSERT INTO dim_users (user_id, partner_id, username, email, full_name, country, city, segment, lifetime_value, signup_date, last_active_date, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 'US', 'TestCity', 'standard', 0, NOW(), NOW(), NOW(), NOW())
			ON CONFLICT (user_id) DO NOTHING
		`, userID, partnerID, username, email, fullName)

		if err != nil {
			return nil, fmt.Errorf("failed to insert user %d: %w", userID, err)
		}

		userIDs = append(userIDs, userID)
	}

	log.Printf("[Seed] Successfully created %d users for partner %d", len(userIDs), partnerID)
	return userIDs, nil
}

// Realistic product catalog for demo
var realisticProducts = []struct {
	Name     string
	Category string
	Price    float64
}{
	// Electronics
	{"MacBook Pro 14\"", "Electronics", 1999.00},
	{"iPhone 15 Pro", "Electronics", 1199.00},
	{"iPad Air", "Electronics", 599.00},
	{"AirPods Pro", "Electronics", 249.00},
	{"Apple Watch Ultra", "Electronics", 799.00},
	{"Samsung Galaxy S24", "Electronics", 899.00},
	{"Sony WH-1000XM5", "Electronics", 349.00},
	{"Dell XPS 15", "Electronics", 1499.00},
	{"Nintendo Switch OLED", "Electronics", 349.00},
	{"Bose QuietComfort", "Electronics", 279.00},
	// Clothing
	{"Nike Air Max 90", "Clothing", 129.00},
	{"Levi's 501 Jeans", "Clothing", 89.00},
	{"Patagonia Fleece", "Clothing", 149.00},
	{"Adidas Ultraboost", "Clothing", 189.00},
	{"North Face Jacket", "Clothing", 249.00},
	// Home
	{"Dyson V15 Vacuum", "Home", 749.00},
	{"Instant Pot Duo", "Home", 89.00},
	{"Nespresso Vertuo", "Home", 199.00},
	{"Roomba i7+", "Home", 599.00},
	{"Philips Hue Starter", "Home", 179.00},
	// Sports
	{"Peloton Bike+", "Sports", 2495.00},
	{"Garmin Fenix 7", "Sports", 699.00},
	{"Yeti Cooler", "Sports", 299.00},
	{"Theragun Pro", "Sports", 449.00},
	{"Hydro Flask 32oz", "Sports", 44.00},
	// Books/Media
	{"Kindle Paperwhite", "Books", 139.00},
	{"Audible Subscription", "Books", 14.95},
	{"MasterClass Annual", "Books", 180.00},
}

// SeedProducts creates test products for a given partner
// Returns the list of created product IDs
func SeedProducts(db *sql.DB, partnerID int, count int) ([]int, error) {
	if count <= 0 {
		return []int{}, nil
	}

	// Start product IDs from partnerID * 100 to avoid conflicts
	baseProductID := partnerID * 100
	productIDs := make([]int, 0, count)

	log.Printf("[Seed] Creating %d products for partner %d (product_id range: %d-%d)",
		count, partnerID, baseProductID, baseProductID+count-1)

	for i := 0; i < count; i++ {
		productID := baseProductID + i

		// Use realistic product data, cycling through the catalog
		product := realisticProducts[i%len(realisticProducts)]

		sku := fmt.Sprintf("SKU-%s-%d", product.Category[:3], productID)
		productName := product.Name
		category := product.Category
		price := product.Price
		cost := price * 0.6 // 40% margin
		margin := (price - cost) / price

		_, err := db.Exec(`
			INSERT INTO dim_products (product_id, partner_id, sku, product_name, category, price, cost, margin, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
			ON CONFLICT (product_id) DO NOTHING
		`, productID, partnerID, sku, productName, category, price, cost, margin)

		if err != nil {
			return nil, fmt.Errorf("failed to insert product %d: %w", productID, err)
		}

		productIDs = append(productIDs, productID)
	}

	log.Printf("[Seed] Successfully created %d products for partner %d", len(productIDs), partnerID)
	return productIDs, nil
}

// GetValidUserIDs queries existing user IDs for a given partner
func GetValidUserIDs(db *sql.DB, partnerID int) ([]int, error) {
	// Query users in the expected range for this partner
	baseUserID := partnerID * 1000
	endUserID := baseUserID + 999

	rows, err := db.Query(`
		SELECT user_id FROM dim_users
		WHERE partner_id = $1 AND (user_id >= $2 AND user_id <= $3)
		ORDER BY user_id
	`, partnerID, baseUserID, endUserID)

	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	userIDs := make([]int, 0)
	for rows.Next() {
		var userID int
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("failed to scan user_id: %w", err)
		}
		userIDs = append(userIDs, userID)
	}

	return userIDs, nil
}

// GetValidProductIDs queries existing product IDs for a given partner
func GetValidProductIDs(db *sql.DB, partnerID int) ([]int, error) {
	rows, err := db.Query(`
		SELECT product_id FROM dim_products
		WHERE partner_id = $1 OR product_id BETWEEN $2 AND $3
		ORDER BY product_id
	`, partnerID, partnerID*100, partnerID*100+999)

	if err != nil {
		return nil, fmt.Errorf("failed to query products: %w", err)
	}
	defer rows.Close()

	productIDs := make([]int, 0)
	for rows.Next() {
		var productID int
		if err := rows.Scan(&productID); err != nil {
			return nil, fmt.Errorf("failed to scan product_id: %w", err)
		}
		productIDs = append(productIDs, productID)
	}

	return productIDs, nil
}

// CleanupTestData removes test data for a given partner
func CleanupTestData(db *sql.DB, partnerID int) error {
	log.Printf("[Cleanup] Removing test data for partner %d", partnerID)

	// Delete in reverse FK dependency order
	_, err := db.Exec(`DELETE FROM fact_order_items WHERE partner_id = $1`, partnerID)
	if err != nil {
		return fmt.Errorf("failed to delete order items: %w", err)
	}

	_, err = db.Exec(`DELETE FROM fact_orders WHERE partner_id = $1`, partnerID)
	if err != nil {
		return fmt.Errorf("failed to delete orders: %w", err)
	}

	// Delete users in the partner's range
	baseUserID := partnerID * 1000
	endUserID := baseUserID + 999
	_, err = db.Exec(`DELETE FROM dim_users WHERE user_id >= $1 AND user_id <= $2`, baseUserID, endUserID)
	if err != nil {
		return fmt.Errorf("failed to delete users: %w", err)
	}

	// Delete products in the partner's range
	baseProductID := partnerID * 100
	endProductID := baseProductID + 999
	_, err = db.Exec(`DELETE FROM dim_products WHERE product_id >= $1 AND product_id <= $2`, baseProductID, endProductID)
	if err != nil {
		return fmt.Errorf("failed to delete products: %w", err)
	}

	log.Printf("[Cleanup] Successfully cleaned up test data for partner %d", partnerID)
	return nil
}
