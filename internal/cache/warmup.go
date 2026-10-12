package cache

import (
	"context"
	"fmt"
	"log"
	"time"
)

// LoadWarmCache loads the top-K hot entries from Postgres into L1 and L2a.
// This is typically called on service startup during Phase 4 logic.
func (c *Coordinator) LoadWarmCache(ctx context.Context, limit int) error {
	// Query for most accessed entries
	query := `
		SELECT id, tenant_id, query_raw, query_normalized, query_hash, query_domain, answer, 
		       embed_model, embed_version, ttl_seconds, created_at, last_accessed_at, access_count
		FROM cache_entries
		ORDER BY access_count DESC, last_accessed_at DESC
		LIMIT $1
	`
	rows, err := c.l2b.pool.Query(ctx, query, limit)
	if err != nil {
		return fmt.Errorf("warmup query failed: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var e CacheEntry
		err := rows.Scan(
			&e.ID, &e.TenantID, &e.QueryRaw, &e.QueryNormalized, &e.QueryHash, &e.QueryDomain, &e.Answer,
			&e.EmbedModel, &e.EmbedVersion, &e.TTLSeconds, &e.CreatedAt, &e.LastAccessedAt, &e.AccessCount,
		)
		if err != nil {
			log.Printf("Warmup: error scanning entry: %v", err)
			continue
		}

		// Don't load expired entries into L1/L2a
		if e.IsExpired() {
			continue
		}

		// Backfill L1 and L2a synchronously for warmup using QueryHash
		key := e.QueryHash
		if key == "" {
			key = e.QueryNormalized
		}
		if key != "" {
			c.l1.Set(e.TenantID, key, e.Answer, 1*time.Hour)
			if c.l2a != nil {
				_ = c.l2a.Set(ctx, e.TenantID, key, e.Answer, 24*time.Hour)
			}
			count++
		}
	}

	log.Printf("Warmup: loaded %d hot entries into cache tiers", count)
	return nil
}
