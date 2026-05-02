-- Materialized views the RAG API expects (entity index, triple index,
-- structural neighbors). With our static text-only corpus they remain empty
-- (no LLM entity extraction is shipped), but they MUST EXIST so /api/v1/search
-- and /api/v1/knowledge-graph don't 500 with "Table not found".

USE rag_db;

-- ============================================================
-- MV 1: Entity index (entity_name → chunk_ids)
-- ============================================================
DROP MATERIALIZED VIEW IF EXISTS mv_entity_index;
CREATE MATERIALIZED VIEW mv_entity_index
BUILD IMMEDIATE REFRESH AUTO ON COMMIT
DISTRIBUTED BY HASH(entity_name) BUCKETS 4
PROPERTIES('replication_num' = '1')
AS
SELECT
    tenant_id,
    corpus_id,
    entity_name,
    COLLECT_SET(row_id) AS chunk_ids,
    COUNT(*) AS freq
FROM rag_unified
LATERAL VIEW EXPLODE(entity_names) t AS entity_name
WHERE entity_name IS NOT NULL
  AND LENGTH(entity_name) > 2
GROUP BY tenant_id, corpus_id, entity_name
HAVING COUNT(*) < 500;

-- ============================================================
-- MV 2: Triple index (source → relation → target → chunk_id)
-- ============================================================
DROP MATERIALIZED VIEW IF EXISTS mv_triple_index;
CREATE MATERIALIZED VIEW mv_triple_index
BUILD IMMEDIATE REFRESH AUTO ON COMMIT
DISTRIBUTED BY HASH(source_entity) BUCKETS 4
PROPERTIES('replication_num' = '1')
AS
SELECT
    tenant_id,
    corpus_id,
    row_id AS chunk_id,
    CAST(JSON_EXTRACT(rel, '$.source')   AS STRING) AS source_entity,
    CAST(JSON_EXTRACT(rel, '$.relation') AS STRING) AS relation_type,
    CAST(JSON_EXTRACT(rel, '$.target')   AS STRING) AS target_entity
FROM rag_unified
LATERAL VIEW EXPLODE(
    CAST(JSON_EXTRACT(relationships, '$') AS ARRAY<JSON>)
) t AS rel
WHERE relationships IS NOT NULL
  AND JSON_LENGTH(relationships) > 0;

-- ============================================================
-- MV 3: Structural neighbors (chunk → ±2 chunks in same doc)
-- ============================================================
DROP MATERIALIZED VIEW IF EXISTS mv_structural_neighbors;
CREATE MATERIALIZED VIEW mv_structural_neighbors
BUILD IMMEDIATE REFRESH AUTO ON COMMIT
DISTRIBUTED BY HASH(src_chunk) BUCKETS 4
PROPERTIES('replication_num' = '1')
AS
SELECT
    a.tenant_id,
    a.corpus_id,
    a.row_id AS src_chunk,
    COLLECT_SET(b.row_id) AS neighbor_chunks
FROM rag_unified a
JOIN rag_unified b ON
    a.tenant_id = b.tenant_id
    AND a.corpus_id = b.corpus_id
    AND a.doc_id = b.doc_id
    AND a.doc_id IS NOT NULL
    AND ABS(a.chunk_index - b.chunk_index) BETWEEN 1 AND 2
    AND a.row_id != b.row_id
GROUP BY a.tenant_id, a.corpus_id, a.row_id;
