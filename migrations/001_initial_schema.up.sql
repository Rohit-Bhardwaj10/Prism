-- ============================================================
--  Migration 001: Initial schema
--  Applies automatically on first postgres container start
--  (mounted into /docker-entrypoint-initdb.d/)
-- ============================================================

-- pgvector extension — must be created before any VECTOR column
CREATE EXTENSION IF NOT EXISTS vector;

-- ============================================================
--  Table: cache_entries
--  Central store for all cached query/answer pairs.
--  One row per unique (tenant_id, normalized_query) that has
--  been answered by the backend.
-- ============================================================
CREATE TABLE IF NOT EXISTS cache_entries (
    id               BIGSERIAL    PRIMARY KEY,
    tenant_id        TEXT         NOT NULL DEFAULT 'default',
    query_raw        TEXT         NOT NULL,
    query_normalized TEXT         NOT NULL,
    query_hash       TEXT         NOT NULL,
    query_domain     TEXT         NOT NULL DEFAULT 'general',
    embedding        VECTOR(768)  NOT NULL,
    embed_model      TEXT         NOT NULL DEFAULT 'nomic-embed-text',
    embed_version    TEXT         NOT NULL DEFAULT 'v1',
    answer           TEXT         NOT NULL,
    ttl_seconds      INTEGER      NOT NULL DEFAULT 3600,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_accessed_at TIMESTAMPTZ,
    access_count     INTEGER      NOT NULL DEFAULT 0,
    UNIQUE(tenant_id, query_hash)
);

-- ── Indexes ──────────────────────────────────────────────────

-- HNSW index for approximate nearest-neighbour vector search.
-- vector_cosine_ops drives the <=> distance operator used in L2b.
-- HNSW gives ~5ms lookup vs exact scan which is O(n×768).
CREATE INDEX IF NOT EXISTS cache_entries_embedding_hnsw_idx
    ON cache_entries
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Compound index for per-tenant domain lookups and analytics.
CREATE INDEX IF NOT EXISTS cache_entries_tenant_domain_idx
    ON cache_entries (tenant_id, query_domain);

-- Maintenance: TTL expiry sweeps scan by created_at.
CREATE INDEX IF NOT EXISTS cache_entries_created_at_idx
    ON cache_entries (created_at);

-- Warmup query: top N entries by access_count per tenant.
CREATE INDEX IF NOT EXISTS cache_entries_tenant_access_idx
    ON cache_entries (tenant_id, access_count DESC);

-- ── Autovacuum tuning ────────────────────────────────────────
-- cache_entries is a high-churn table (frequent inserts + updates
-- to last_accessed_at/access_count). Default autovacuum thresholds
-- (20% of table) are too large — we tune them down to avoid
-- excessive dead-tuple bloat in the HNSW index.
ALTER TABLE cache_entries SET (
    autovacuum_vacuum_scale_factor  = 0.01,   -- vacuum after 1% dead tuples
    autovacuum_analyze_scale_factor = 0.005   -- analyze after 0.5%
);

-- ============================================================
--  Table: audit_log
--  Append-only record of every cache decision.
--  Never updated after insert. No raw query text stored (PII).
-- ============================================================
CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL    PRIMARY KEY,
    request_id  TEXT         NOT NULL,
    tenant_id   TEXT         NOT NULL,

    -- SHA-256 hash of the normalized query — no PII stored.
    query_hash  TEXT         NOT NULL,

    domain      TEXT,

    -- Decision taken: L1_hit | L2a_hit | L2b_accept | L2b_reject | backend
    decision    TEXT         NOT NULL,

    -- Human-readable reason for the decision (policy rejection cause, etc.)
    reason      TEXT,

    -- Confidence score at the time of the L2b decision (NULL for L1/L2a hits).
    confidence  REAL,

    -- Source IP for rate-abuse investigation (retained for 90 days then purged).
    client_ip   TEXT,

    ts          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS audit_log_tenant_ts_idx
    ON audit_log (tenant_id, ts DESC);

-- ============================================================
--  Table: feedback
--  Signals from clients about answer quality.
--  Used by the Phase 5 adaptive threshold tuner.
-- ============================================================
CREATE TABLE IF NOT EXISTS feedback (
    id          BIGSERIAL    PRIMARY KEY,
    request_id  TEXT         NOT NULL,
    tenant_id   TEXT         NOT NULL,
    correct     BOOLEAN      NOT NULL,   -- true = answer was correct
    reason      TEXT,                    -- optional free-text from client
    ts          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS feedback_tenant_ts_idx
    ON feedback (tenant_id, ts DESC);
