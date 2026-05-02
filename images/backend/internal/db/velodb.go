package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"velodb-demo/backend/internal/config"
	"velodb-demo/backend/internal/models"
)

// Real product names for dashboard display (matching ecommerce catalog)
var realProductNames = []struct {
	Category string
	Name     string
}{
	{"Men's Clothing", "T-shirt - basic"},
	{"Men's Clothing", "Polo shirt - navy"},
	{"Men's Clothing", "Jeans - slim fit"},
	{"Men's Clothing", "Hoodie - black"},
	{"Men's Clothing", "Sweater - grey"},
	{"Men's Clothing", "Jacket - casual"},
	{"Men's Shoes", "Casual lace-ups - sand"},
	{"Men's Shoes", "Trainers - white"},
	{"Men's Shoes", "Boots - leather brown"},
	{"Men's Shoes", "Loafers - suede"},
	{"Men's Shoes", "Running shoes"},
	{"Men's Accessories", "Laptop bag - black/brown"},
	{"Men's Accessories", "Watch - silver"},
	{"Men's Accessories", "Belt - leather"},
	{"Men's Accessories", "Wallet - black"},
	{"Women's Clothing", "Dress - summer floral"},
	{"Women's Clothing", "Blouse - silk"},
	{"Women's Clothing", "Jeans - high waist"},
	{"Women's Clothing", "Cardigan - cashmere"},
	{"Women's Clothing", "Coat - winter"},
	{"Women's Shoes", "Heels - stiletto black"},
	{"Women's Shoes", "Flats - ballet"},
	{"Women's Shoes", "Boots - ankle"},
	{"Women's Shoes", "Sneakers - platform"},
	{"Women's Shoes", "Sandals - strappy"},
	{"Women's Accessories", "Handbag - tote"},
	{"Women's Accessories", "Earrings - gold"},
	{"Women's Accessories", "Necklace - pendant"},
	{"Women's Accessories", "Scarf - printed"},
	{"Women's Accessories", "Watch - rose gold"},
}

// GetRealProductName returns a real product name for a given product_id
// Uses deterministic mapping based on product_id modulo catalog size
func GetRealProductName(productID int) string {
	if len(realProductNames) == 0 {
		return fmt.Sprintf("Product %d", productID)
	}
	index := productID % len(realProductNames)
	return realProductNames[index].Name
}

// GetRealProductCategory returns a real product category for a given product_id
// Uses deterministic mapping based on product_id modulo catalog size
func GetRealProductCategory(productID int) string {
	if len(realProductNames) == 0 {
		return "Uncategorized"
	}
	index := productID % len(realProductNames)
	return realProductNames[index].Category
}

type VeloDBClient struct {
	db *sql.DB
}

// NewVeloDBClient creates a new VeloDB client with connection pooling
func NewVeloDBClient(cfg *config.VeloDBConfig) (*VeloDBClient, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open VeloDB connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection with retries
	for i := 0; i < 3; i++ {
		if err := db.Ping(); err == nil {
			break
		}
		if i == 2 {
			return nil, fmt.Errorf("failed to ping VeloDB after 3 attempts: %w", err)
		}
		time.Sleep(2 * time.Second)
	}

	return &VeloDBClient{db: db}, nil
}

// Close closes the VeloDB connection
func (c *VeloDBClient) Close() error {
	return c.db.Close()
}

// Ping checks if the VeloDB connection is alive
func (c *VeloDBClient) Ping() error {
	return c.db.Ping()
}

// GetDB returns the underlying sql.DB instance
func (c *VeloDBClient) GetDB() *sql.DB {
	return c.db
}

// GetRevenueMetrics retrieves hourly revenue metrics from kibana_sample_data_ecommerce
// Filters by partner_id for multi-tenant support
func (c *VeloDBClient) GetRevenueMetrics(partnerID int, hours int) ([]models.RevenueMetrics, error) {
	query := `
		SELECT
			DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00') as hour,
			SUM(taxful_total_price) as revenue,
			COUNT(*) as order_count,
			AVG(taxful_total_price) as avg_order_value,
			(SUM(taxful_total_price) - LAG(SUM(taxful_total_price)) OVER (ORDER BY DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00'))) /
				NULLIF(LAG(SUM(taxful_total_price)) OVER (ORDER BY DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00')), 0) as growth_rate
		FROM kibana_sample_data_ecommerce
		WHERE order_date >= DATE_SUB(NOW(), INTERVAL ? HOUR)
			AND partner_id = ?
		GROUP BY DATE_FORMAT(order_date, '%Y-%m-%d %H:00:00')
		ORDER BY hour DESC
	`

	rows, err := c.db.Query(query, hours, partnerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query revenue metrics: %w", err)
	}
	defer rows.Close()

	var metrics []models.RevenueMetrics
	for rows.Next() {
		var m models.RevenueMetrics
		var hourStr string
		var growthRate sql.NullFloat64

		if err := rows.Scan(&hourStr, &m.Revenue, &m.OrderCount, &m.AvgOrderValue, &growthRate); err != nil {
			return nil, fmt.Errorf("failed to scan revenue metrics: %w", err)
		}

		m.Hour, _ = time.Parse("2006-01-02 15:04:05", hourStr)
		if growthRate.Valid {
			m.GrowthRate = growthRate.Float64
		}

		metrics = append(metrics, m)
	}

	return metrics, nil
}

// GetConversionFunnel retrieves pre-aggregated clickstream conversion funnel from materialized view
// The MV mv_conversion_funnel is refreshed every 1 minute and provides 7-day rolling window
func (c *VeloDBClient) GetConversionFunnel(partnerID int, days int) (*models.ConversionFunnel, error) {
	// Query the materialized view for pre-aggregated funnel data
	// MV refresh: EVERY 1 MINUTE, covers last 7 days
	// Performance: ~85ms (MV) vs ~144ms (base table scan) - 40% faster
	// As data grows, base table gets slower while MV stays constant
	query := `
		SELECT
			COALESCE(views, 0) as views,
			COALESCE(carts, 0) as carts,
			COALESCE(purchases, 0) as purchases
		FROM mv_conversion_funnel
		WHERE partner_id = ?
	`

	var funnel models.ConversionFunnel
	funnel.PartnerID = partnerID
	funnel.PeriodDays = days

	err := c.db.QueryRow(query, partnerID).Scan(&funnel.Views, &funnel.Carts, &funnel.Purchases)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversion funnel from MV: %w", err)
	}

	// Calculate conversion rates
	if funnel.Views > 0 {
		funnel.ViewToCartRate = float64(funnel.Carts) / float64(funnel.Views)
	}
	if funnel.Carts > 0 {
		funnel.CartToPurchaseRate = float64(funnel.Purchases) / float64(funnel.Carts)
	}

	return &funnel, nil
}

// GetProductIntelligence retrieves top products from kibana_sample_data_ecommerce
// Filters by partner_id for multi-tenant support
// Products are extracted from the JSON products column
func (c *VeloDBClient) GetProductIntelligence(partnerID int, limit int) ([]models.ProductIntelligence, error) {
	query := fmt.Sprintf(`
		WITH current_week AS (
			SELECT
				get_json_string(products, '$[0].product_name') as product_name,
				get_json_string(products, '$[0].category') as category,
				SUM(CAST(get_json_double(products, '$[0].taxful_price') AS DECIMAL(12,2))) as revenue
			FROM kibana_sample_data_ecommerce
			WHERE order_date >= DATE_SUB(NOW(), INTERVAL 7 DAY)
				AND partner_id = %d
			GROUP BY
				get_json_string(products, '$[0].product_name'),
				get_json_string(products, '$[0].category')
		),
		prev_week AS (
			SELECT
				get_json_string(products, '$[0].product_name') as product_name,
				SUM(CAST(get_json_double(products, '$[0].taxful_price') AS DECIMAL(12,2))) as revenue
			FROM kibana_sample_data_ecommerce
			WHERE order_date >= DATE_SUB(NOW(), INTERVAL 14 DAY)
				AND order_date < DATE_SUB(NOW(), INTERVAL 7 DAY)
				AND partner_id = %d
			GROUP BY get_json_string(products, '$[0].product_name')
		),
		ranked AS (
			SELECT
				cw.product_name,
				cw.category,
				cw.revenue,
				DENSE_RANK() OVER (ORDER BY cw.revenue DESC) as current_rank,
				pw.revenue as prev_revenue,
				DENSE_RANK() OVER (ORDER BY pw.revenue DESC) as prev_rank
			FROM current_week cw
			LEFT JOIN prev_week pw ON cw.product_name = pw.product_name
		)
		SELECT
			0 as product_id,
			r.product_name as name,
			r.category,
			r.revenue,
			r.current_rank,
			r.prev_rank,
			CASE WHEN r.prev_revenue > 0
				THEN (r.revenue - r.prev_revenue) / r.prev_revenue
				ELSE 0
			END as growth_rate
		FROM ranked r
		WHERE r.current_rank <= %d
		ORDER BY r.current_rank
	`, partnerID, partnerID, limit)

	rows, err := c.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query product intelligence: %w", err)
	}
	defer rows.Close()

	var products []models.ProductIntelligence
	for rows.Next() {
		var p models.ProductIntelligence
		var prevRank sql.NullInt64
		var category sql.NullString

		if err := rows.Scan(&p.ProductID, &p.Name, &category, &p.Revenue, &p.Rank, &prevRank, &p.GrowthRate); err != nil {
			return nil, fmt.Errorf("failed to scan product intelligence: %w", err)
		}

		// Use category from query result
		if category.Valid {
			p.Category = category.String
		} else {
			p.Category = "Uncategorized"
		}

		if prevRank.Valid {
			rank := int(prevRank.Int64)
			p.PrevRank = &rank
			p.RankChange = rank - p.Rank
		}

		products = append(products, p)
	}

	return products, nil
}

// CheckOrderExists checks if an order exists in VeloDB's kibana_sample_data_ecommerce
// and returns the order_date when found
func (c *VeloDBClient) CheckOrderExists(orderID int) (bool, time.Time, error) {
	query := `SELECT order_date FROM kibana_sample_data_ecommerce WHERE order_id = ?`

	var orderDate time.Time
	err := c.db.QueryRow(query, orderID).Scan(&orderDate)
	if err == sql.ErrNoRows {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, fmt.Errorf("failed to check order existence: %w", err)
	}

	return true, orderDate, nil
}

// GetRecentClickstream retrieves recent clickstream events for a partner
// VeloDB schema: event_id, event_time, partner_id, session_id, user_id, event_type,
//                page_url, product_name, product_category, product_price, referrer,
//                user_agent, device_type, country, city
func (c *VeloDBClient) GetRecentClickstream(partnerID int, limit int) ([]models.RecentClickstream, error) {
	query := fmt.Sprintf(`
		SELECT
			event_id,
			event_type,
			user_id,
			CONCAT('{"product_name":"', IFNULL(product_name, ''), '","category":"', IFNULL(product_category, ''), '","price":', IFNULL(product_price, 0), '}') as event_properties,
			DATE_FORMAT(event_time, '%%Y-%%m-%%d %%H:%%i:%%s') as event_timestamp
		FROM fact_clickstream
		WHERE partner_id = %d
		ORDER BY event_time DESC
		LIMIT %d`, partnerID, limit)

	rows, err := c.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent clickstream: %w", err)
	}
	defer rows.Close()

	var events []models.RecentClickstream
	for rows.Next() {
		var event models.RecentClickstream
		var eventPropsRaw, eventTimestampRaw []byte
		if err := rows.Scan(&event.EventID, &event.EventType, &event.UserID, &eventPropsRaw, &eventTimestampRaw); err != nil {
			return nil, fmt.Errorf("failed to scan clickstream event: %w", err)
		}
		event.EventProperties = string(eventPropsRaw)
		event.EventTimestamp = string(eventTimestampRaw)
		events = append(events, event)
	}

	return events, nil
}

// GetRecentEcommerceOrders retrieves recent ecommerce orders from VeloDB
// Filters by partner_id for multi-tenant support
// Uses EcommerceOrder type defined in postgres.go for compatibility
func (c *VeloDBClient) GetRecentEcommerceOrders(partnerID int, limit int) ([]EcommerceOrder, error) {
	query := fmt.Sprintf(`
		SELECT
			order_id,
			order_date,
			customer_id,
			IFNULL(customer_first_name, '') as customer_first_name,
			IFNULL(customer_last_name, '') as customer_last_name,
			IFNULL(customer_full_name, '') as customer_full_name,
			IFNULL(customer_gender, '') as customer_gender,
			IFNULL(email, '') as email,
			IFNULL(currency, 'USD') as currency,
			IFNULL(taxful_total_price, 0) as taxful_total_price,
			IFNULL(taxless_total_price, 0) as taxless_total_price,
			IFNULL(total_quantity, 0) as total_quantity,
			IFNULL(total_unique_products, 0) as total_unique_products,
			IFNULL(order_status, 'pending') as order_status,
			IFNULL(category, '[]') as category,
			IFNULL(manufacturer, '[]') as manufacturer,
			IFNULL(products, '[]') as products,
			IFNULL(geoip, '{}') as geoip
		FROM kibana_sample_data_ecommerce
		WHERE partner_id = %d
		ORDER BY order_date DESC
		LIMIT %d`, partnerID, limit)

	rows, err := c.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent ecommerce orders: %w", err)
	}
	defer rows.Close()

	var orders []EcommerceOrder
	for rows.Next() {
		var o EcommerceOrder
		var categoryRaw, manufacturerRaw, productsRaw, geoipRaw []byte

		if err := rows.Scan(
			&o.OrderID,
			&o.OrderDate,
			&o.CustomerID,
			&o.CustomerFirstName,
			&o.CustomerLastName,
			&o.CustomerFullName,
			&o.CustomerGender,
			&o.Email,
			&o.Currency,
			&o.TaxfulTotalPrice,
			&o.TaxlessTotalPrice,
			&o.TotalQuantity,
			&o.TotalUniqueProducts,
			&o.OrderStatus,
			&categoryRaw,
			&manufacturerRaw,
			&productsRaw,
			&geoipRaw,
		); err != nil {
			return nil, fmt.Errorf("failed to scan ecommerce order: %w", err)
		}

		// Use OrderID as ID since VeloDB doesn't have auto-increment ID
		o.ID = int(o.OrderID)
		o.Category = string(categoryRaw)
		o.Manufacturer = string(manufacturerRaw)
		o.Products = string(productsRaw)
		o.GeoIP = string(geoipRaw)

		orders = append(orders, o)
	}

	return orders, nil
}

// GetEcommerceStats retrieves aggregated ecommerce statistics from VeloDB
// Filters by partner_id for multi-tenant support
// Uses EcommerceStats type defined in postgres.go for compatibility
func (c *VeloDBClient) GetEcommerceStats(partnerID int, hours int) (*EcommerceStats, error) {
	// Get aggregate stats
	query := `
		SELECT
			COUNT(*) as total_orders,
			IFNULL(SUM(taxful_total_price), 0) as total_revenue,
			IFNULL(AVG(taxful_total_price), 0) as avg_order_value
		FROM kibana_sample_data_ecommerce
		WHERE partner_id = ?
			AND order_date >= DATE_SUB(NOW(), INTERVAL ? HOUR)
	`

	var stats EcommerceStats
	err := c.db.QueryRow(query, partnerID, hours).Scan(
		&stats.TotalOrders,
		&stats.TotalRevenue,
		&stats.AvgOrderValue,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query ecommerce stats: %w", err)
	}

	// Initialize empty slices
	stats.TopCategories = []CategoryStats{}
	stats.OrdersByHour = []HourlyStats{}

	// Get hourly breakdown for Revenue Over Time chart
	hourlyQuery := fmt.Sprintf(`
		SELECT
			DATE_FORMAT(order_date, '%%Y-%%m-%%dT%%H:00:00Z') as hour,
			IFNULL(SUM(taxful_total_price), 0) as revenue,
			COUNT(*) as orders
		FROM kibana_sample_data_ecommerce
		WHERE partner_id = %d
			AND order_date >= DATE_SUB(NOW(), INTERVAL %d HOUR)
		GROUP BY DATE_FORMAT(order_date, '%%Y-%%m-%%dT%%H:00:00Z')
		ORDER BY hour DESC
	`, partnerID, hours)

	rows, err := c.db.Query(hourlyQuery)
	if err != nil {
		// Don't fail the whole request if hourly query fails
		return &stats, nil
	}
	defer rows.Close()

	for rows.Next() {
		var h HourlyStats
		if err := rows.Scan(&h.Hour, &h.Revenue, &h.Orders); err != nil {
			continue
		}
		stats.OrdersByHour = append(stats.OrdersByHour, h)
	}

	return &stats, nil
}

// EventsPerMinute represents clickstream events aggregated by minute
type EventsPerMinute struct {
	Minute string `json:"minute"`
	Count  int    `json:"count"`
}

// GetEventsPerMinute retrieves clickstream event counts per minute from VeloDB
// This is useful for visualizing spike effects in real-time charts
func (c *VeloDBClient) GetEventsPerMinute(partnerID int, minutes int) ([]EventsPerMinute, error) {
	query := fmt.Sprintf(`
		SELECT
			DATE_FORMAT(event_time, '%%H:%%i') as minute,
			COUNT(*) as count
		FROM fact_clickstream
		WHERE partner_id = %d
			AND event_time >= DATE_SUB(NOW(), INTERVAL %d MINUTE)
		GROUP BY DATE_FORMAT(event_time, '%%Y-%%m-%%d %%H:%%i')
		ORDER BY minute ASC
	`, partnerID, minutes)

	rows, err := c.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query events per minute: %w", err)
	}
	defer rows.Close()

	var results []EventsPerMinute
	for rows.Next() {
		var e EventsPerMinute
		if err := rows.Scan(&e.Minute, &e.Count); err != nil {
			continue
		}
		results = append(results, e)
	}

	return results, nil
}

// RevenuePerMinute represents revenue and orders aggregated by minute
type RevenuePerMinute struct {
	Minute  string  `json:"minute"`
	Revenue float64 `json:"revenue"`
	Orders  int     `json:"orders"`
}

// GetRevenuePerMinute retrieves revenue and order counts per minute from VeloDB
// This is useful for visualizing spike effects in real-time charts
func (c *VeloDBClient) GetRevenuePerMinute(partnerID int, minutes int) ([]RevenuePerMinute, error) {
	query := fmt.Sprintf(`
		SELECT
			DATE_FORMAT(order_date, '%%H:%%i') as minute,
			COALESCE(SUM(taxful_total_price), 0) as revenue,
			COUNT(*) as orders
		FROM kibana_sample_data_ecommerce
		WHERE partner_id = %d
			AND order_date >= DATE_SUB(NOW(), INTERVAL %d MINUTE)
		GROUP BY minute
		ORDER BY minute ASC
	`, partnerID, minutes)

	rows, err := c.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query revenue per minute: %w", err)
	}
	defer rows.Close()

	var results []RevenuePerMinute
	for rows.Next() {
		var r RevenuePerMinute
		if err := rows.Scan(&r.Minute, &r.Revenue, &r.Orders); err != nil {
			continue
		}
		results = append(results, r)
	}

	return results, nil
}
