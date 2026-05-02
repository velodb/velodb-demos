-- PostgreSQL TTL Cleanup Configuration
-- Automatically deletes data older than DATA_RETENTION_DAYS (default: 3 days)
-- This runs as a stored procedure that can be called by cron or external scheduler

-- Create cleanup function
CREATE OR REPLACE FUNCTION cleanup_old_data(retention_days INTEGER DEFAULT 3)
RETURNS TABLE(
    table_name TEXT,
    deleted_count BIGINT
) AS $$
DECLARE
    ecommerce_deleted BIGINT;
BEGIN
    -- Delete old ecommerce orders
    DELETE FROM kibana_sample_data_ecommerce
    WHERE order_date < NOW() - (retention_days || ' days')::INTERVAL;

    GET DIAGNOSTICS ecommerce_deleted = ROW_COUNT;

    -- Return summary
    table_name := 'kibana_sample_data_ecommerce';
    deleted_count := ecommerce_deleted;
    RETURN NEXT;

    -- Log cleanup
    RAISE NOTICE 'Cleanup completed: deleted % rows from kibana_sample_data_ecommerce (retention: % days)',
        ecommerce_deleted, retention_days;
END;
$$ LANGUAGE plpgsql;

-- Create a view to check data age distribution
CREATE OR REPLACE VIEW data_age_summary AS
SELECT
    'kibana_sample_data_ecommerce' AS table_name,
    COUNT(*) AS total_rows,
    COUNT(*) FILTER (WHERE order_date >= NOW() - INTERVAL '1 day') AS last_1_day,
    COUNT(*) FILTER (WHERE order_date >= NOW() - INTERVAL '3 days') AS last_3_days,
    COUNT(*) FILTER (WHERE order_date >= NOW() - INTERVAL '7 days') AS last_7_days,
    COUNT(*) FILTER (WHERE order_date < NOW() - INTERVAL '3 days') AS older_than_3_days,
    MIN(order_date) AS oldest_record,
    MAX(order_date) AS newest_record
FROM kibana_sample_data_ecommerce;

-- Grant execute permission
GRANT EXECUTE ON FUNCTION cleanup_old_data TO demo_user;

-- Usage instructions
COMMENT ON FUNCTION cleanup_old_data IS 'Delete data older than specified days. Usage: SELECT * FROM cleanup_old_data(3);';

-- Example: Run cleanup for 3 days retention
-- SELECT * FROM cleanup_old_data(3);

-- Example: Check data age distribution
-- SELECT * FROM data_age_summary;

SELECT 'TTL cleanup function installed. Use: SELECT * FROM cleanup_old_data(3);' AS status;
