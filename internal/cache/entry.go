package cache

import "time"

// CacheEntry represents a single cached query-answer pair stored in Postgres.
// Fields mirror the cache_entries table columns defined in 001_initial_schema.sql
// and extended by migration 003_add_proxy_fields.sql.
type CacheEntry struct {
	ID              int64     `db:"id"`
	TenantID        string    `db:"tenant_id"`
	QueryRaw        string    `db:"query_raw"`
	QueryNormalized string    `db:"query_normalized"`
	QueryHash       string    `db:"query_hash"`
	QueryDomain     string    `db:"query_domain"`
	Answer          string    `db:"answer"`
	// Embedding is omitted here; retrieved only when needed for similarity search.
	EmbedModel      string    `db:"embed_model"`
	EmbedVersion    string    `db:"embed_version"`
	Similarity      float32   `db:"-"` // populated at query time, not stored
	Confidence      float32   `db:"-"` // computed by policy engine, not stored
	TTLSeconds      int       `db:"ttl_seconds"`
	CreatedAt       time.Time `db:"created_at"`
	LastAccessedAt  time.Time `db:"last_accessed_at"`
	AccessCount     int       `db:"access_count"`

	// Proxy metadata — added by migration 003_add_proxy_fields.sql.
	// Legacy entries (from /cache/query) use DEFAULT values:
	//   Provider="legacy", Model="", PromptTokens=0, CompletionTokens=0.
	Provider         string `db:"provider"`          // "openai" | "anthropic" | "groq" | "together" | "legacy"
	Model            string `db:"model"`             // exact model string reported by provider
	PromptTokens     int    `db:"prompt_tokens"`     // input tokens (0 for legacy entries)
	CompletionTokens int    `db:"completion_tokens"` // output tokens (0 for legacy entries)
}

// IsExpired reports whether this entry has outlived its TTL.
// A TTL of 0 means the entry never expires.
func (e *CacheEntry) IsExpired() bool {
	if e.TTLSeconds == 0 {
		return false
	}
	expiresAt := e.CreatedAt.Add(time.Duration(e.TTLSeconds) * time.Second)
	return time.Now().After(expiresAt)
}

// AgeSeconds returns the number of seconds since the entry was created.
func (e *CacheEntry) AgeSeconds() int {
	d := time.Since(e.CreatedAt)
	if d < 0 {
		return 0
	}
	return int(d.Seconds())
}
