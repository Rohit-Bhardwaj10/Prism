-- ============================================================
--  Migration 003: Add proxy provider/model/token columns
--  These columns are populated by the /proxy/* handlers.
--  Legacy rows (from /cache/query) keep their DEFAULT values
--  and receive the x_cache_usage_estimated: true flag on replay.
-- ============================================================

ALTER TABLE cache_entries
    ADD COLUMN IF NOT EXISTS provider          TEXT    NOT NULL DEFAULT 'legacy',
    ADD COLUMN IF NOT EXISTS model             TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS completion_tokens INTEGER NOT NULL DEFAULT 0;

-- Index for per-provider analytics queries (e.g. cost attribution by provider+model).
CREATE INDEX IF NOT EXISTS cache_entries_provider_model_idx
    ON cache_entries (provider, model);

COMMENT ON COLUMN cache_entries.provider IS
    'Upstream LLM provider: openai | anthropic | groq | together | legacy';
COMMENT ON COLUMN cache_entries.model IS
    'Exact model string reported by provider on cache write (e.g. gpt-4o, claude-3-5-sonnet-20241022)';
COMMENT ON COLUMN cache_entries.prompt_tokens IS
    'Input token count as reported by provider. 0 for legacy entries.';
COMMENT ON COLUMN cache_entries.completion_tokens IS
    'Output token count as reported by provider. 0 for legacy entries.';
