-- VeloDB Demo - PostgreSQL Schema Initialization
-- Real-time Customer-Facing Analytics Demo
--
-- Primary table: kibana_sample_data_ecommerce
-- - Kibana-compatible ecommerce orders for rich dashboard visualization
-- - Supports CDC demo (INSERT/UPDATE/DELETE)
-- - Denormalized for analytics (embedded customer, products, geo data)

-- ============================================================================
-- DROP LEGACY TABLES (no longer needed)
-- ============================================================================

DROP TABLE IF EXISTS fact_order_items CASCADE;
DROP TABLE IF EXISTS fact_orders CASCADE;
DROP TABLE IF EXISTS dim_products CASCADE;
DROP TABLE IF EXISTS dim_users CASCADE;

-- ============================================================================
-- ECOMMERCE ORDERS TABLE (Primary Demo Table)
-- ============================================================================

-- kibana_sample_data_ecommerce: Real-time ecommerce orders
-- Schema matches Kibana's sample ecommerce data format for native dashboard support
CREATE TABLE IF NOT EXISTS kibana_sample_data_ecommerce (
    id SERIAL PRIMARY KEY,
    order_date TIMESTAMP(3) NOT NULL,
    order_id BIGINT NOT NULL UNIQUE,
    partner_id INTEGER NOT NULL DEFAULT 44,

    -- Customer Information (embedded for analytics)
    customer_id BIGINT NOT NULL,
    customer_first_name TEXT,
    customer_last_name TEXT,
    customer_full_name TEXT,
    customer_gender TEXT,  -- MALE/FEMALE
    customer_phone TEXT,
    email TEXT,
    "user" TEXT,  -- Username

    -- Order Metadata
    day_of_week TEXT,      -- Monday, Tuesday, etc.
    day_of_week_i INTEGER, -- 0-6
    currency TEXT,         -- EUR, USD, GBP

    -- Order Totals
    taxful_total_price DOUBLE PRECISION,
    taxless_total_price DOUBLE PRECISION,
    total_quantity INTEGER,
    total_unique_products INTEGER,

    -- Order Status (for CDC demo: pending -> shipped -> delivered)
    order_status TEXT DEFAULT 'pending',
    type TEXT DEFAULT 'order',

    -- Arrays/Objects (stored as JSONB)
    category JSONB,        -- ["Men's Clothing", "Women's Shoes"]
    manufacturer JSONB,    -- ["Nike", "Adidas"]
    sku JSONB,             -- ["SKU123", "SKU456"]
    products JSONB,        -- Nested product details
    geoip JSONB,           -- {city_name, country_iso_code, location: {lat, lon}}
    event JSONB,           -- {dataset: "sample_ecommerce"}

    -- Timestamps for CDC
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for real-time analytics queries
CREATE INDEX IF NOT EXISTS idx_ecommerce_order_date ON kibana_sample_data_ecommerce(order_date DESC);
CREATE INDEX IF NOT EXISTS idx_ecommerce_order_id ON kibana_sample_data_ecommerce(order_id);
CREATE INDEX IF NOT EXISTS idx_ecommerce_partner_id ON kibana_sample_data_ecommerce(partner_id);
CREATE INDEX IF NOT EXISTS idx_ecommerce_customer_id ON kibana_sample_data_ecommerce(customer_id);
CREATE INDEX IF NOT EXISTS idx_ecommerce_order_status ON kibana_sample_data_ecommerce(order_status);
CREATE INDEX IF NOT EXISTS idx_ecommerce_currency ON kibana_sample_data_ecommerce(currency);
CREATE INDEX IF NOT EXISTS idx_ecommerce_created_at ON kibana_sample_data_ecommerce(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ecommerce_updated_at ON kibana_sample_data_ecommerce(updated_at DESC);

-- GIN index for JSONB queries (category, manufacturer searches)
CREATE INDEX IF NOT EXISTS idx_ecommerce_category ON kibana_sample_data_ecommerce USING GIN (category);
CREATE INDEX IF NOT EXISTS idx_ecommerce_manufacturer ON kibana_sample_data_ecommerce USING GIN (manufacturer);

-- ============================================================================
-- TRIGGERS FOR AUTOMATIC TIMESTAMP UPDATES
-- ============================================================================

CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger for ecommerce table
DROP TRIGGER IF EXISTS update_ecommerce_updated_at ON kibana_sample_data_ecommerce;
CREATE TRIGGER update_ecommerce_updated_at
    BEFORE UPDATE ON kibana_sample_data_ecommerce
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ============================================================================
-- VERIFICATION
-- ============================================================================

SELECT 'Schema initialized successfully' AS status;
SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename;

VACUUM ANALYZE;
