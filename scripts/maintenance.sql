-- ──────────────────────────────────────────────────────────────
--  maintenance.sql — prism Periodic Maintenance
-- ──────────────────────────────────────────────────────────────

-- 1. Purge expired entries based on TTL
-- Note: coordinator also checks IsExpired(), but this keeps the DB lean.
DELETE FROM cache_entries 
WHERE (created_at + (ttl_seconds * INTERVAL '1 second')) < NOW();

-- 2. Concurrently reindex the HNSW vector index
-- Improves performance after many writes/deletes without blocking readers.
REINDEX INDEX CONCURRENTLY cache_entries_embedding_idx;

-- 3. Update table statistics for the query planner
VACUUM ANALYZE cache_entries;

-- 4. Clean up audit logs older than 90 days (if applicable)
DELETE FROM audit_log WHERE ts < NOW() - INTERVAL '90 days';
