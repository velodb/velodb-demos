-- RAG-Anything: Single-Table Architecture (Demo Stage)
--
-- Bootstrapped by dist/scripts/rag-load.sh against the local Apache Doris.
-- The chunks dataset (15 docs, 82 chunks, BGE-M3 1024-d embeddings) is
-- pre-computed and shipped at dist/rag/data/chunks.json.

CREATE DATABASE IF NOT EXISTS rag_db;
USE rag_db;

-- This simplified schema uses ONE table for ALL content types:
-- - Text chunks
-- - Images (with paths)
-- - Tables (with markdown)
-- - Formulas (with LaTeX)
--
-- Benefits:
-- - Single query returns all data (no JOINs)
-- - Inline entities (no separate entity table)
-- - Pre-computed neighbors (no runtime graph traversal)
-- - <300ms retrieval latency
--
-- For production, consider the full schema in schema.sql

-- ============================================================
-- CORE TABLE: rag_unified - Single table for ALL content
-- ============================================================
-- (Idempotent: do NOT drop the table on re-runs — the loader uses CREATE TABLE
-- IF NOT EXISTS and the bootstrap skips re-loading when rows already exist.)

CREATE TABLE IF NOT EXISTS rag_unified (
    -- Keys (multi-tenant)
    tenant_id           VARCHAR(64) NOT NULL DEFAULT 'default',
    corpus_id           VARCHAR(64) NOT NULL DEFAULT 'default',
    row_id              VARCHAR(64) NOT NULL,

    -- Content
    content_type        VARCHAR(20) NOT NULL,  -- 'text', 'image', 'table', 'formula'
    content             TEXT,                   -- Text content or description
    content_embedding   ARRAY<FLOAT> NOT NULL,  -- BGE-M3 1024-dim embedding

    -- Searchable entity arrays (ARRAY + INVERTED = O(log n) search)
    entity_names        ARRAY<STRING>,          -- ['d_k', 'transformer', 'attention']
    entity_types        ARRAY<STRING>,          -- ['PARAMETER', 'MODEL', 'CONCEPT']

    -- Full entity data (JSON for Doris 3.x, upgradeable to VARIANT in 4.x)
    entities            JSON,                   -- [{name, type, properties}, ...]
    relationships       JSON,                   -- [{source, target, relation}, ...]

    -- Multimodal source paths for provenance/citation
    multimodal_data     JSON,                   -- {type, path, caption, latex, ...}

    -- Pre-computed graph (O(1) traversal, no runtime BFS)
    neighbor_ids        ARRAY<VARCHAR(64)>,     -- 1-hop neighbor chunk IDs
    related_entity_names ARRAY<STRING>,         -- Entities in neighbors (for 2-hop)

    -- Metadata
    doc_id              VARCHAR(64),            -- Source document ID
    chunk_index         INT,                    -- Position in document
    page_number         INT,                    -- Source page number
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,

    -- INVERTED Indexes for sub-300ms retrieval
    INDEX idx_content (content) USING INVERTED PROPERTIES("parser" = "english"),
    INDEX idx_entity_names (entity_names) USING INVERTED,
    INDEX idx_entity_types (entity_types) USING INVERTED,
    INDEX idx_related_entities (related_entity_names) USING INVERTED,
    INDEX idx_content_type (content_type) USING INVERTED
) ENGINE=OLAP
DUPLICATE KEY(tenant_id, corpus_id, row_id)
DISTRIBUTED BY HASH(row_id) BUCKETS 16
PROPERTIES("replication_num" = "1");

-- ============================================================
-- QUERY PATTERNS (Reference)
-- ============================================================

-- 1. Hybrid Search (Vector + BM25 with RRF fusion)
-- SELECT row_id, content, entities, multimodal_data, neighbor_ids,
--        (1.0/(60+vec_rank) + 1.0/(60+bm25_rank)) as rrf
-- FROM (
--     SELECT *,
--         ROW_NUMBER() OVER (ORDER BY l2_distance(content_embedding, ?)) as vec_rank,
--         ROW_NUMBER() OVER (ORDER BY score() DESC) as bm25_rank
--     FROM rag_unified
--     WHERE tenant_id = ? AND corpus_id = ?
--       AND content MATCH ?
-- ) t
-- ORDER BY rrf DESC LIMIT 10;

-- 2. Entity Search (O(log n) via INVERTED index)
-- SELECT * FROM rag_unified
-- WHERE array_contains(entity_names, 'vector_search');

-- 3. Graph Expansion (O(1) via pre-computed arrays)
-- SELECT * FROM rag_unified
-- WHERE row_id IN (SELECT UNNEST(neighbor_ids) FROM rag_unified WHERE row_id = ?);

-- 4. 2-Hop Entity Search (No extra query)
-- SELECT * FROM rag_unified
-- WHERE array_contains(related_entity_names, 'softmax');

-- 5. Content Type Filter
-- SELECT * FROM rag_unified
-- WHERE content_type = 'image' AND array_contains(entity_names, 'architecture');

-- ============================================================
-- SUBGRAPHRAG: Already Supported by rag_unified
-- ============================================================
-- The unified table already has everything needed for SubgraphRAG:
--
-- 1. Seed Entity Finding:
--    WHERE array_contains(entity_names, 'transformer')
--    → Uses INVERTED index, O(log n)
--
-- 2. 1-Hop Expansion:
--    WHERE row_id IN (SELECT UNNEST(neighbor_ids) FROM rag_unified WHERE row_id = ?)
--    → Uses pre-computed neighbor_ids, O(1)
--
-- 3. 2-Hop Entity Search:
--    WHERE array_contains(related_entity_names, 'softmax')
--    → Uses pre-computed related_entity_names, O(log n)
--
-- 4. Triple Access:
--    SELECT relationships FROM rag_unified WHERE row_id = ?
--    → JSON column with [{source, target, relation}, ...]
--
-- NO ADDITIONAL TABLES NEEDED for demo/MVP stage.
-- Add MVs only when scale requires it (>1M chunks).
