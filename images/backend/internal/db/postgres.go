package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"velodb-demo/backend/internal/config"
	"velodb-demo/backend/internal/models"
)

// Customer name data for generating realistic names
var (
	firstNamesMale = []string{
		"Thad", "Eddie", "George", "Oliver", "Recip", "Phil", "Boris", "Jim",
		"Yusuf", "Tariq", "Abdul", "Sultan", "Irwin", "Jackson", "William", "Robert",
	}
	firstNamesFemale = []string{
		"Mary", "Diane", "Betty", "Stephanie", "Gwen", "Clarice", "Brigitte", "Elyssa",
		"Rabbia", "Yasmine", "Wilhemina", "Selena", "Sonya", "Kayla", "Frances", "Abigail",
	}
	lastNames = []string{
		"Thompson", "Graham", "Underwood", "Byrd", "Rivera", "Bryant", "Powell", "Berry",
		"Perkins", "Wise", "Shaw", "Hunter", "Lloyd", "Long", "Hayes", "Burns",
	}
)

// GetCustomerName returns a deterministic real customer name based on user_id
func GetCustomerName(userID int) string {
	// Use userID to deterministically pick names
	// Alternate between male and female names
	var firstName string
	if userID%2 == 0 {
		firstName = firstNamesMale[userID%len(firstNamesMale)]
	} else {
		firstName = firstNamesFemale[userID%len(firstNamesFemale)]
	}
	lastName := lastNames[(userID/2)%len(lastNames)]
	return fmt.Sprintf("%s %s", firstName, lastName)
}

type PostgresClient struct {
	db *sql.DB
}

// NewPostgresClient creates a new Postgres client with connection pooling
func NewPostgresClient(cfg *config.PostgresConfig) (*PostgresClient, error) {
	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open Postgres connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping Postgres: %w", err)
	}

	return &PostgresClient{db: db}, nil
}

// Close closes the Postgres connection
func (c *PostgresClient) Close() error {
	return c.db.Close()
}

// Ping checks if the Postgres connection is alive
func (c *PostgresClient) Ping() error {
	return c.db.Ping()
}

// GetDB returns the underlying database connection
func (c *PostgresClient) GetDB() *sql.DB {
	return c.db
}

// CreateOrder creates a new order in the database
func (c *PostgresClient) CreateOrder(order *models.Order) (int, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Calculate total amount
	var totalAmount float64
	for _, item := range order.Items {
		totalAmount += item.UnitPrice * float64(item.Quantity)
	}

	// Insert order
	var orderID int
	err = tx.QueryRow(`
		INSERT INTO fact_orders (partner_id, user_id, total_amount, order_status, order_date)
		VALUES ($1, $2, $3, 'pending', NOW())
		RETURNING order_id
	`, order.PartnerID, order.UserID, totalAmount).Scan(&orderID)

	if err != nil {
		return 0, fmt.Errorf("failed to insert order: %w", err)
	}

	// Insert order items
	for _, item := range order.Items {
		lineTotal := item.UnitPrice * float64(item.Quantity)
		_, err = tx.Exec(`
			INSERT INTO fact_order_items (partner_id, order_id, product_id, quantity, unit_price, line_total)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, order.PartnerID, orderID, item.ProductID, item.Quantity, item.UnitPrice, lineTotal)

		if err != nil {
			return 0, fmt.Errorf("failed to insert order item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return orderID, nil
}

// GetPartnerMetadata retrieves partner metadata
func (c *PostgresClient) GetPartnerMetadata(partnerID int) (*models.Partner, error) {
	var partner models.Partner

	err := c.db.QueryRow(`
		SELECT partner_id, name, tier, status
		FROM dim_partners
		WHERE partner_id = $1
	`, partnerID).Scan(&partner.PartnerID, &partner.Name, &partner.Tier, &partner.Status)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("partner not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query partner metadata: %w", err)
	}

	return &partner, nil
}

// UpdateOrderStatus updates the status of an order
func (c *PostgresClient) UpdateOrderStatus(orderID int, status string) error {
	result, err := c.db.Exec(`
		UPDATE fact_orders
		SET order_status = $1
		WHERE order_id = $2
	`, status, orderID)

	if err != nil {
		return fmt.Errorf("failed to update order status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("order not found")
	}

	return nil
}

// GetOrder retrieves an order by ID
func (c *PostgresClient) GetOrder(orderID int) (*models.Order, error) {
	var order models.Order

	err := c.db.QueryRow(`
		SELECT order_id, partner_id, user_id, order_status, order_date
		FROM fact_orders
		WHERE order_id = $1
	`, orderID).Scan(&order.OrderID, &order.PartnerID, &order.UserID, &order.Status, &order.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("order not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query order: %w", err)
	}

	// Get order items
	rows, err := c.db.Query(`
		SELECT product_id, quantity, unit_price
		FROM fact_order_items
		WHERE order_id = $1
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query order items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item models.OrderItem
		if err := rows.Scan(&item.ProductID, &item.Quantity, &item.UnitPrice); err != nil {
			return nil, fmt.Errorf("failed to scan order item: %w", err)
		}
		order.Items = append(order.Items, item)
	}

	return &order, nil
}

// GetRecentOrders retrieves recent orders for a partner
func (c *PostgresClient) GetRecentOrders(partnerID int, limit int) ([]models.RecentOrder, error) {
	rows, err := c.db.Query(`
		SELECT order_id, user_id, total_amount, order_status, order_date
		FROM fact_orders
		WHERE partner_id = $1
		ORDER BY order_date DESC
		LIMIT $2
	`, partnerID, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to query recent orders: %w", err)
	}
	defer rows.Close()

	var orders []models.RecentOrder
	for rows.Next() {
		var order models.RecentOrder
		if err := rows.Scan(&order.OrderID, &order.UserID, &order.TotalAmount, &order.Status, &order.OrderDate); err != nil {
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		// Add real customer name based on user_id
		order.CustomerName = GetCustomerName(order.UserID)
		orders = append(orders, order)
	}

	return orders, nil
}

// EcommerceOrder represents an ecommerce order with real product names (Kibana format)
type EcommerceOrder struct {
	ID                  int       `json:"id"`
	OrderID             int64     `json:"order_id"`
	OrderDate           time.Time `json:"order_date"`
	CustomerID          int64     `json:"customer_id"`
	CustomerFirstName   string    `json:"customer_first_name"`
	CustomerLastName    string    `json:"customer_last_name"`
	CustomerFullName    string    `json:"customer_full_name"`
	CustomerGender      string    `json:"customer_gender"`
	Email               string    `json:"email"`
	Currency            string    `json:"currency"`
	TaxfulTotalPrice    float64   `json:"taxful_total_price"`
	TaxlessTotalPrice   float64   `json:"taxless_total_price"`
	TotalQuantity       int       `json:"total_quantity"`
	TotalUniqueProducts int       `json:"total_unique_products"`
	OrderStatus         string    `json:"order_status"`
	Category            string    `json:"category"`    // JSON array as string
	Manufacturer        string    `json:"manufacturer"` // JSON array as string
	Products            string    `json:"products"`     // JSON array as string
	GeoIP               string    `json:"geoip"`        // JSON object as string
}

// GetRecentEcommerceOrders retrieves recent ecommerce orders with real product names
// Filters by partner_id for multi-tenant support
func (c *PostgresClient) GetRecentEcommerceOrders(partnerID int, limit int) ([]EcommerceOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	rows, err := c.db.Query(`
		SELECT
			id,
			order_id,
			order_date,
			customer_id,
			COALESCE(customer_first_name, '') as customer_first_name,
			COALESCE(customer_last_name, '') as customer_last_name,
			COALESCE(customer_full_name, '') as customer_full_name,
			COALESCE(customer_gender, '') as customer_gender,
			COALESCE(email, '') as email,
			COALESCE(currency, 'USD') as currency,
			COALESCE(taxful_total_price, 0) as taxful_total_price,
			COALESCE(taxless_total_price, 0) as taxless_total_price,
			COALESCE(total_quantity, 0) as total_quantity,
			COALESCE(total_unique_products, 0) as total_unique_products,
			COALESCE(order_status, 'pending') as order_status,
			COALESCE(category::text, '[]') as category,
			COALESCE(manufacturer::text, '[]') as manufacturer,
			COALESCE(products::text, '[]') as products,
			COALESCE(geoip::text, '{}') as geoip
		FROM kibana_sample_data_ecommerce
		WHERE partner_id = $1
		ORDER BY order_date DESC
		LIMIT $2
	`, partnerID, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to query ecommerce orders: %w", err)
	}
	defer rows.Close()

	var orders []EcommerceOrder
	for rows.Next() {
		var order EcommerceOrder
		if err := rows.Scan(
			&order.ID,
			&order.OrderID,
			&order.OrderDate,
			&order.CustomerID,
			&order.CustomerFirstName,
			&order.CustomerLastName,
			&order.CustomerFullName,
			&order.CustomerGender,
			&order.Email,
			&order.Currency,
			&order.TaxfulTotalPrice,
			&order.TaxlessTotalPrice,
			&order.TotalQuantity,
			&order.TotalUniqueProducts,
			&order.OrderStatus,
			&order.Category,
			&order.Manufacturer,
			&order.Products,
			&order.GeoIP,
		); err != nil {
			return nil, fmt.Errorf("failed to scan ecommerce order: %w", err)
		}
		orders = append(orders, order)
	}

	return orders, nil
}

// GetEcommerceOrderByID retrieves a single ecommerce order by order_id
func (c *PostgresClient) GetEcommerceOrderByID(orderID int64) (*EcommerceOrder, error) {
	var order EcommerceOrder
	err := c.db.QueryRow(`
		SELECT
			id,
			order_id,
			order_date,
			customer_id,
			COALESCE(customer_first_name, '') as customer_first_name,
			COALESCE(customer_last_name, '') as customer_last_name,
			COALESCE(customer_full_name, '') as customer_full_name,
			COALESCE(customer_gender, '') as customer_gender,
			COALESCE(email, '') as email,
			COALESCE(currency, 'USD') as currency,
			COALESCE(taxful_total_price, 0) as taxful_total_price,
			COALESCE(taxless_total_price, 0) as taxless_total_price,
			COALESCE(total_quantity, 0) as total_quantity,
			COALESCE(total_unique_products, 0) as total_unique_products,
			COALESCE(order_status, 'pending') as order_status,
			COALESCE(category::text, '[]') as category,
			COALESCE(manufacturer::text, '[]') as manufacturer,
			COALESCE(products::text, '[]') as products,
			COALESCE(geoip::text, '{}') as geoip
		FROM kibana_sample_data_ecommerce
		WHERE order_id = $1
	`, orderID).Scan(
		&order.ID,
		&order.OrderID,
		&order.OrderDate,
		&order.CustomerID,
		&order.CustomerFirstName,
		&order.CustomerLastName,
		&order.CustomerFullName,
		&order.CustomerGender,
		&order.Email,
		&order.Currency,
		&order.TaxfulTotalPrice,
		&order.TaxlessTotalPrice,
		&order.TotalQuantity,
		&order.TotalUniqueProducts,
		&order.OrderStatus,
		&order.Category,
		&order.Manufacturer,
		&order.Products,
		&order.GeoIP,
	)

	if err == sql.ErrNoRows {
		return nil, nil // Return nil, nil for not found (handler will return 404)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query ecommerce order: %w", err)
	}

	return &order, nil
}

// UpdateEcommerceOrderStatus updates the status of an ecommerce order
func (c *PostgresClient) UpdateEcommerceOrderStatus(orderID int64, status string) error {
	result, err := c.db.Exec(`
		UPDATE kibana_sample_data_ecommerce
		SET order_status = $1
		WHERE order_id = $2
	`, status, orderID)

	if err != nil {
		return fmt.Errorf("failed to update ecommerce order status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("order not found")
	}

	return nil
}

// DeleteEcommerceOrder hard deletes an ecommerce order from the database
func (c *PostgresClient) DeleteEcommerceOrder(orderID int64) error {
	result, err := c.db.Exec(`
		DELETE FROM kibana_sample_data_ecommerce
		WHERE order_id = $1
	`, orderID)

	if err != nil {
		return fmt.Errorf("failed to delete ecommerce order: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("order not found")
	}

	return nil
}

// EcommerceStats represents aggregated ecommerce statistics
type EcommerceStats struct {
	TotalRevenue    float64           `json:"total_revenue"`
	TotalOrders     int               `json:"total_orders"`
	AvgOrderValue   float64           `json:"avg_order_value"`
	TopCategories   []CategoryStats   `json:"top_categories"`
	OrdersByHour    []HourlyStats     `json:"orders_by_hour"`
}

// CategoryStats represents stats for a single category
type CategoryStats struct {
	Category string  `json:"category"`
	Revenue  float64 `json:"revenue"`
	Orders   int     `json:"orders"`
}

// HourlyStats represents orders in an hour
type HourlyStats struct {
	Hour    string  `json:"hour"`
	Orders  int     `json:"orders"`
	Revenue float64 `json:"revenue"`
}

// GetEcommerceStats retrieves aggregated ecommerce statistics for the past N hours
// Filters by partner_id for multi-tenant support
func (c *PostgresClient) GetEcommerceStats(partnerID int, hours int) (*EcommerceStats, error) {
	if hours <= 0 {
		hours = 24
	}

	stats := &EcommerceStats{
		TopCategories: []CategoryStats{},
		OrdersByHour:  []HourlyStats{},
	}

	// Get total revenue, total orders, avg order value
	err := c.db.QueryRow(`
		SELECT
			COALESCE(SUM(taxful_total_price), 0) as total_revenue,
			COUNT(*) as total_orders,
			COALESCE(AVG(taxful_total_price), 0) as avg_order_value
		FROM kibana_sample_data_ecommerce
		WHERE order_date >= NOW() - INTERVAL '1 hour' * $1
			AND partner_id = $2
	`, hours, partnerID).Scan(&stats.TotalRevenue, &stats.TotalOrders, &stats.AvgOrderValue)
	if err != nil {
		return nil, fmt.Errorf("failed to get summary stats: %w", err)
	}

	// Get top categories (extract from JSON array and aggregate)
	categoryRows, err := c.db.Query(`
		SELECT
			cat,
			SUM(taxful_total_price) as revenue,
			COUNT(*) as orders
		FROM kibana_sample_data_ecommerce,
			LATERAL jsonb_array_elements_text(category) as cat
		WHERE order_date >= NOW() - INTERVAL '1 hour' * $1
			AND partner_id = $2
		GROUP BY cat
		ORDER BY revenue DESC
		LIMIT 10
	`, hours, partnerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get top categories: %w", err)
	}
	defer categoryRows.Close()

	for categoryRows.Next() {
		var cs CategoryStats
		if err := categoryRows.Scan(&cs.Category, &cs.Revenue, &cs.Orders); err != nil {
			return nil, fmt.Errorf("failed to scan category stats: %w", err)
		}
		stats.TopCategories = append(stats.TopCategories, cs)
	}

	// Get orders by hour
	hourlyRows, err := c.db.Query(`
		SELECT
			TO_CHAR(DATE_TRUNC('hour', order_date), 'YYYY-MM-DD HH24:00') as hour,
			COUNT(*) as orders,
			COALESCE(SUM(taxful_total_price), 0) as revenue
		FROM kibana_sample_data_ecommerce
		WHERE order_date >= NOW() - INTERVAL '1 hour' * $1
			AND partner_id = $2
		GROUP BY DATE_TRUNC('hour', order_date)
		ORDER BY hour DESC
		LIMIT 24
	`, hours, partnerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get hourly stats: %w", err)
	}
	defer hourlyRows.Close()

	for hourlyRows.Next() {
		var hs HourlyStats
		if err := hourlyRows.Scan(&hs.Hour, &hs.Orders, &hs.Revenue); err != nil {
			return nil, fmt.Errorf("failed to scan hourly stats: %w", err)
		}
		stats.OrdersByHour = append(stats.OrdersByHour, hs)
	}

	return stats, nil
}

// DeleteOrder deletes an order and its items from the database (hard delete)
func (c *PostgresClient) DeleteOrder(orderID int) error {
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete order items first (foreign key constraint)
	_, err = tx.Exec(`DELETE FROM fact_order_items WHERE order_id = $1`, orderID)
	if err != nil {
		return fmt.Errorf("failed to delete order items: %w", err)
	}

	// Delete the order
	result, err := tx.Exec(`DELETE FROM fact_orders WHERE order_id = $1`, orderID)
	if err != nil {
		return fmt.Errorf("failed to delete order: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("order not found")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
