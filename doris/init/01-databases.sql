-- Bootstrap databases for VeloDB Demo (Apache Doris 4.1.0)

-- Set a root password so downstream services don't run with empty creds.
-- The password used here MUST match VELODB_PASSWORD / DORIS_PASSWORD in compose.
SET PASSWORD FOR 'root' = PASSWORD('VeloDB@demo123');

CREATE DATABASE IF NOT EXISTS us_demo;
CREATE DATABASE IF NOT EXISTS demo_observability;

-- Single-replica defaults for the laptop demo
ALTER DATABASE us_demo SET PROPERTIES("replication_allocation" = "tag.location.default: 1");
ALTER DATABASE demo_observability SET PROPERTIES("replication_allocation" = "tag.location.default: 1");
