package cache

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// L2bCache handles semantic similarity search in Postgres using pgvector.
type L2bCache struct {
	pool         *pgxpool.Pool
	embedModel   string
	embedVersion string
}

func NewL2bCache(pool *pgxpool.Pool, model, version string) *L2bCache {
	return &L2bCache{
		pool:         pool,
		embedModel:   model,
		embedVersion: version,
	}
}

// Search finds the top-K semantically similar entries for a given embedding.
func (c *L2bCache) Search(ctx context.Context, tenantID string, embedding []float32, limit int) ([]*CacheEntry, error) {
	// Standard COSINE similarity search:
	// <=> is the cosine distance operator. 
	// (1 - distance) = similarity.
	query := `
		SELECT id, tenant_id, query_raw, query_normalized, query_hash, query_domain, answer,
		       embed_model, embed_version, ttl_seconds, (1 - (embedding <=> $1)) AS similarity,
		       created_at, last_accessed_at, access_count,
		       provider, model, prompt_tokens, completion_tokens
		FROM cache_entries
		WHERE tenant_id = $2
		  AND embed_model = $3
		  AND embed_version = $4
		ORDER BY embedding <=> $1
		LIMIT $5
	`

	rows, err := c.pool.Query(ctx, query, embedding, tenantID, c.embedModel, c.embedVersion, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres search error: %w", err)
	}
	defer rows.Close()

	var entries []*CacheEntry
	for rows.Next() {
		var e CacheEntry
		err := rows.Scan(
			&e.ID, &e.TenantID, &e.QueryRaw, &e.QueryNormalized, &e.QueryHash, &e.QueryDomain, &e.Answer,
			&e.EmbedModel, &e.EmbedVersion, &e.TTLSeconds, &e.Similarity,
			&e.CreatedAt, &e.LastAccessedAt, &e.AccessCount,
			&e.Provider, &e.Model, &e.PromptTokens, &e.CompletionTokens,
		)
		if err != nil {
			return nil, fmt.Errorf("error scanning cache entry: %w", err)
		}
		entries = append(entries, &e)
	}

	return entries, nil
}

// Write stores a result in the Postgres cache.
func (c *L2bCache) Write(ctx context.Context, e *CacheEntry, embedding []float32) error {
	// Resolve provider — never store empty string; fall back to "legacy".
	provider := e.Provider
	if provider == "" {
		provider = "legacy"
	}
	query := `
		INSERT INTO cache_entries (
			tenant_id, query_raw, query_normalized, query_hash, query_domain, answer,
			embedding, embed_model, embed_version, ttl_seconds,
			created_at, last_accessed_at, access_count,
			provider, model, prompt_tokens, completion_tokens
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW(), 1, $11, $12, $13, $14)
		ON CONFLICT (tenant_id, query_hash) DO UPDATE SET
			last_accessed_at = NOW(),
			access_count = cache_entries.access_count + 1
	`
	_, err := c.pool.Exec(ctx, query,
		e.TenantID, e.QueryRaw, e.QueryNormalized, e.QueryHash, e.QueryDomain, e.Answer,
		embedding, c.embedModel, c.embedVersion, e.TTLSeconds,
		provider, e.Model, e.PromptTokens, e.CompletionTokens,
	)
	if err != nil {
		return fmt.Errorf("postgres write error: %w", err)
	}
	return nil
}
// GetStoredVersions returns the distinct embedding models and versions currently in the cache.
func (c *L2bCache) GetStoredVersions(ctx context.Context) ([]struct{ Model, Version string }, error) {
	query := `SELECT DISTINCT embed_model, embed_version FROM cache_entries`
	rows, err := c.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query stored versions: %w", err)
	}
	defer rows.Close()

	var versions []struct{ Model, Version string }
	for rows.Next() {
		var v struct{ Model, Version string }
		if err := rows.Scan(&v.Model, &v.Version); err != nil {
			return nil, fmt.Errorf("error scanning version: %w", err)
		}
		versions = append(versions, v)
	}
	return versions, nil
}
