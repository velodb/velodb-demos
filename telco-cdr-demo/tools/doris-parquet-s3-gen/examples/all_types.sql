-- One column of every Doris type this tool generates, for trying the
-- Parquet mapping end to end:
--
--   doris-parquet-s3-gen --schema examples/all_types.sql --out-dir /tmp/all --rows 100000
--
-- DECIMAL above 38 digits needs `enable_decimal256` on the cluster.
CREATE TABLE IF NOT EXISTS demo.all_types
(
    `id`          BIGINT          NOT NULL,
    `c_bool`      BOOLEAN,
    `c_tinyint`   TINYINT,
    `c_smallint`  SMALLINT,
    `c_int`       INT,
    `c_largeint`  LARGEINT,
    `c_float`     FLOAT,
    `c_double`    DOUBLE,
    `c_dec_9`     DECIMAL(9, 2),
    `c_dec_38`    DECIMAL(38, 10),
    `c_dec_76`    DECIMAL(76, 20),
    `c_date`      DATE,
    `c_datetime`  DATETIME(3),
    `c_char`      CHAR(17),
    `c_varchar`   VARCHAR(120),
    `c_string`    STRING,
    `c_json`      JSON,
    `c_variant`   VARIANT,
    `c_ipv4`      IPV4,
    `c_ipv6`      IPV6,
    `c_array`     ARRAY<INT>,
    `c_nested`    ARRAY<ARRAY<VARCHAR(20)>>,
    `c_map`       MAP<VARCHAR(16), DECIMAL(10, 2)>,
    `c_struct`    STRUCT<city:VARCHAR(80) COMMENT 'city name', zip:INT, seen_at:DATETIME(3)>,
    `c_bitmap`    BITMAP          BITMAP_UNION,
    `c_hll`       HLL             HLL_UNION,
    `c_quantile`  QUANTILE_STATE  QUANTILE_UNION
)
ENGINE=OLAP
AGGREGATE KEY(`id`, `c_bool`, `c_tinyint`, `c_smallint`, `c_int`, `c_largeint`)
DISTRIBUTED BY HASH(`id`) BUCKETS 4
PROPERTIES ("replication_num" = "1");
