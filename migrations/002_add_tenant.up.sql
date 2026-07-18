-- ============================================================
--  Migration 002: Add tenant_id column (idempotent)
--  Run AFTER migration 001 is applied.
--
--  ⚠️  DEV WARNING: In a fresh dev environment, just drop and
--  recreate the database — migration 001 already includes
--  tenant_id. Only run this migration against an existing DB
--  that was created before tenant isolation was added.
--
--  Production steps:
--    1. Apply this migration during a maintenance window.
--    2. Backfill completes synchronously — may take time on
--       large tables. Consider batching for > 1M rows.
--    3. Deploy new proxy binary that reads tenant_id.
--    4. Run REINDEX to rebuild scoped indexes.
-- ============================================================

-- Add tenant_id if it doesn't exist (idempotent)
ALTER TABLE cache_entries
    ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default';

-- Backfill any existing rows
UPDATE cache_entries
    SET tenant_id = 'default'
    WHERE tenant_id IS NULL OR tenant_id = '';

-- Add scoped indexes that were in 001 but may be missing
-- on upgraded databases (IF NOT EXISTS makes this safe)
CREATE INDEX IF NOT EXISTS cache_entries_tenant_domain_idx
    ON cache_entries (tenant_id, query_domain);

CREATE INDEX IF NOT EXISTS cache_entries_tenant_access_idx
    ON cache_entries (tenant_id, access_count DESC);

-- Rebuild HNSW index to include any rows added before this migration
-- NOTE: Use REINDEX CONCURRENTLY in production to avoid table lock.
-- REINDEX CONCURRENTLY INDEX cache_entries_embedding_hnsw_idx;

-- ── audit_log tenant column ─────────────────────────────────
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default';

UPDATE audit_log
    SET tenant_id = 'default'
    WHERE tenant_id IS NULL OR tenant_id = '';

CREATE INDEX IF NOT EXISTS audit_log_tenant_ts_idx
    ON audit_log (tenant_id, ts DESC);
