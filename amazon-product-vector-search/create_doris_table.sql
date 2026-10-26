-- Doris / VeloDB schema for the product catalog exported by run-export.sh
-- (Amazon Reviews 2023 product metadata, embedded with BAAI/bge-small-en-v1.5).
-- Column order matches product-export.py (JSON keys are matched by name).
--
-- Vector search: `embedding` carries an HNSW ANN index. Vectors are
-- L2-normalized by product-gen.py, so inner_product == cosine similarity.
-- Hybrid search: the scalar columns can be used in WHERE alongside the ANN
-- ordering, and the text columns carry inverted indexes for MATCH queries.
-- See sample_query.sql for runnable queries.

CREATE DATABASE IF NOT EXISTS hybrid_search_demo;

CREATE TABLE IF NOT EXISTS hybrid_search_demo.products (
    product_id          VARCHAR(32)   NOT NULL COMMENT 'Amazon parent ASIN',
    product_name        VARCHAR(1200) NOT NULL COMMENT 'title (VARCHAR sizes are bytes: 4x the char cap for UTF-8)',
    product_description STRING        NOT NULL COMMENT 'description + feature bullets',
    main_category       VARCHAR(512)  NULL COMMENT 'top-level department, e.g. Amazon Home',
    product_type        VARCHAR(512)  NULL COMMENT 'most specific category, e.g. Mug Sets',
    brand               VARCHAR(512)  NULL,
    color               VARCHAR(512)  NULL,
    material            VARCHAR(512)  NULL,
    style               VARCHAR(512)  NULL,
    categories          ARRAY<STRING> NULL COMMENT 'full category path, root first',
    browse_node         VARCHAR(2048) NULL COMMENT 'category path joined with " / "',
    image_url           VARCHAR(2048) NULL,
    price               DOUBLE        NULL COMMENT 'USD',
    average_rating      DOUBLE        NULL COMMENT '1..5',
    rating_number       INT           NULL COMMENT 'number of ratings',
    weight_lb           DOUBLE        NULL COMMENT 'pounds',
    height_in           DOUBLE        NULL COMMENT 'inches',
    length_in           DOUBLE        NULL COMMENT 'inches',
    width_in            DOUBLE        NULL COMMENT 'inches',
    embedding           ARRAY<FLOAT>  NOT NULL COMMENT 'BAAI/bge-small-en-v1.5, 384-dim, L2-normalized',

    INDEX idx_embedding (embedding) USING ANN PROPERTIES (
        "index_type"  = "hnsw",
        "metric_type" = "inner_product",
        "dim"         = "384"
    ),
    INDEX idx_name        (product_name)        USING INVERTED PROPERTIES ("parser" = "english"),
    INDEX idx_description (product_description) USING INVERTED PROPERTIES ("parser" = "english"),
    INDEX idx_categories  (categories)          USING INVERTED,
    INDEX idx_type        (product_type)        USING INVERTED,
    INDEX idx_brand       (brand)               USING INVERTED,
    INDEX idx_color       (color)               USING INVERTED,
    INDEX idx_material    (material)            USING INVERTED
)
ENGINE = OLAP
DUPLICATE KEY (product_id)   -- ANN indexes require the duplicate-key model
COMMENT 'Amazon Home & Kitchen catalog with BGE embeddings for hybrid vector + filter search'
DISTRIBUTED BY HASH (product_id) BUCKETS 12;
