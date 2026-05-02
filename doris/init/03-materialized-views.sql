-- Asynchronous materialized view for the conversion funnel
-- Auto-refreshes every minute over the last 7 days

USE us_demo;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_conversion_funnel
BUILD IMMEDIATE REFRESH AUTO ON SCHEDULE EVERY 1 MINUTE
PROPERTIES ('enable_nondeterministic_function' = 'true', 'replication_num' = '1')
AS
SELECT
    partner_id,
    SUM(CASE WHEN event_type = 'view' THEN 1 ELSE 0 END) as views,
    SUM(CASE WHEN event_type = 'cart' THEN 1 ELSE 0 END) as carts,
    SUM(CASE WHEN event_type = 'purchase' THEN 1 ELSE 0 END) as purchases
FROM fact_clickstream
WHERE event_time >= DATE_SUB(NOW(), INTERVAL 7 DAY)
GROUP BY partner_id;
