-- E-commerce demo tables on Apache Doris 4.1.0

USE us_demo;

CREATE TABLE IF NOT EXISTS kibana_sample_data_ecommerce (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT 'Auto-increment ID',
    order_id BIGINT NOT NULL COMMENT 'Unique order ID',
    partner_id INT NOT NULL DEFAULT '44' COMMENT 'Partner/tenant ID',
    order_date DATETIME NOT NULL COMMENT 'Order timestamp',
    customer_id BIGINT NOT NULL COMMENT 'Customer ID',
    customer_first_name VARCHAR(100),
    customer_last_name VARCHAR(100),
    customer_full_name VARCHAR(200),
    customer_gender VARCHAR(10),
    customer_phone VARCHAR(50),
    email VARCHAR(200),
    day_of_week VARCHAR(20),
    day_of_week_i TINYINT,
    currency VARCHAR(10),
    taxful_total_price DECIMAL(12,2),
    taxless_total_price DECIMAL(12,2),
    total_quantity INT,
    total_unique_products INT,
    order_status VARCHAR(50) DEFAULT 'pending',

    -- Extra columns the backend's EcommerceSync writes
    `user` VARCHAR(200),
    `type` VARCHAR(50),
    sku TEXT,
    event TEXT,

    category VARCHAR(500),
    manufacturer VARCHAR(500),
    products TEXT,
    geoip TEXT
)
UNIQUE KEY(id, order_id, partner_id)
DISTRIBUTED BY HASH(partner_id) BUCKETS 4
PROPERTIES (
    'replication_num' = '1',
    'enable_unique_key_merge_on_write' = 'true'
);

CREATE TABLE IF NOT EXISTS fact_clickstream (
    event_id VARCHAR(64) NOT NULL,
    event_time DATETIME NOT NULL,
    partner_id INT NOT NULL,
    session_id VARCHAR(64),
    user_id INT,
    event_type VARCHAR(50) NOT NULL,
    page_url VARCHAR(500),
    product_name VARCHAR(200),
    product_category VARCHAR(100),
    product_price DECIMAL(10,2),
    referrer VARCHAR(500),
    user_agent VARCHAR(500),
    device_type VARCHAR(50),
    country VARCHAR(100)
)
DUPLICATE KEY(event_id, event_time, partner_id)
PARTITION BY RANGE(event_time) ()
DISTRIBUTED BY HASH(partner_id) BUCKETS 4
PROPERTIES (
    'replication_num' = '1',
    'dynamic_partition.enable' = 'true',
    'dynamic_partition.time_unit' = 'DAY',
    'dynamic_partition.start' = '-7',
    'dynamic_partition.end' = '3',
    'dynamic_partition.prefix' = 'p',
    'dynamic_partition.buckets' = '4',
    'dynamic_partition.replication_num' = '1'
);
