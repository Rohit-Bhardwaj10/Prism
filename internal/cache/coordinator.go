package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/audit"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/backend"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/metrics"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/policy"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/resilience"
	"github.com/Rohit-Bhardwaj10/semantic-cache/pkg/embeddings"
)

// Coordinator orchestrates the L0 -> L1 -> L2a -> L2b -> Backend flow.
type Coordinator struct {
	normalizer *Normalizer
	l1         *L1Cache
	l2a        *L2aCache
	l2b        *L2bCache
	embeddings *embeddings.Client
	policy     *policy.Engine
	classifier *policy.DomainClassifier
	backend    backend.Backend
	breaker    *resilience.CircuitBreaker
	audit      *audit.Logger
	metrics    *metrics.Metrics

	// Group for deduplicating concurrent backend calls
	sfGroup singleflight.Group
}

// Config holds the dependencies for the Coordinator.
type Config struct {
	Normalizer *Normalizer
	L1         *L1Cache
	L2a        *L2aCache
	L2b        *L2bCache
	Embeddings *embeddings.Client
	Policy     *policy.Engine
	Classifier *policy.DomainClassifier
	Backend    backend.Backend
	Breaker    *resilience.CircuitBreaker
	Audit      *audit.Logger
	Metrics    *metrics.Metrics
}

func NewCoordinator(cfg Config) *Coordinator {
	return &Coordinator{
		normalizer: cfg.Normalizer,
		l1:         cfg.L1,
		l2a:        cfg.L2a,
		l2b:        cfg.L2b,
		embeddings: cfg.Embeddings,
		policy:     cfg.Policy,
		classifier: cfg.Classifier,
		backend:    cfg.Backend,
		breaker:    cfg.Breaker,
		audit:      cfg.Audit,
		metrics:    cfg.Metrics,
	}
}

// QueryRequest represents an incoming user query.
// For the legacy /cache/query endpoint, only Query/TenantID/Domain/RequestID are set.
// For the proxy endpoints, Provider/Model/System/Messages are also populated.
type QueryRequest struct {
	Query     string // raw query text (used by /cache/query)
	TenantID  string
	Domain    string
	RequestID string

	// Proxy-mode fields — populated by proxy handlers only.
	Provider string           // "openai" | "anthropic" | "groq" | "together"
	Model    string           // exact model identifier
	System   string           // system prompt
	Messages []backend.Message // ordered conversation turns
}

// computeCacheKey returns a stable sha256-based key over all request dimensions that affect
// uniqueness: provider, model, system prompt, and the full message array.
// For legacy /cache/query requests (no messages), it hashes the normalized query string instead.
// The result is used as the `query` argument to L1/L2a (both treat it as an opaque key)
// and as CacheEntry.QueryHash for the Postgres UNIQUE(tenant_id, query_hash) constraint.
func computeCacheKey(req QueryRequest, normalized string) string {
	var sb strings.Builder
	sb.WriteString(req.TenantID)
	sb.WriteByte('|')
	sb.WriteString(req.Provider)
	sb.WriteByte('|')
	sb.WriteString(req.Model)
	sb.WriteByte('|')
	sb.WriteString(req.System)
	sb.WriteByte('|')
	for _, m := range req.Messages {
		sb.WriteString(m.Role)
		sb.WriteByte(':')
		sb.WriteString(m.Content)
		sb.WriteByte('\n')
	}
	if len(req.Messages) == 0 {
		// Legacy path: hash the normalized query
		sb.WriteString(normalized)
	}
	hash := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(hash[:])
}

// embeddableText extracts the text to embed for semantic similarity search.
// We embed only user-role messages (optionally prefixed by the system prompt) to ensure
// that queries with identical intent but different assistant histories match semantically.
// Full-conversation hashing is reserved for exact L1/L2a lookups via computeCacheKey.
func embeddableText(req QueryRequest, normalized string) string {
	if len(req.Messages) == 0 {
		// Legacy path: use the already-normalized query string.
		return normalized
	}
	var parts []string
	if req.System != "" {
		parts = append(parts, req.System)
	}
	for _, m := range req.Messages {
		if m.Role == "user" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, " ")
}

// QueryResponse represents the final result returned to the client.
type QueryResponse struct {
	Answer     string
	Source     string
	Hit        bool
	Confidence float32
	LatencyMS  int64
	// Usage carries token counts from the upstream provider (zero for cache hits on legacy entries).
	Usage        backend.Usage
	UsageFromCache bool // true when Usage was replayed from a stored cache entry
}

const (
	// Estimated costs in USD
	CostBackendPerRequest  = 0.02    // GPT-4 scale
	CostEmbeddingPerQuery  = 0.0005  // Ollama inference
	CostInfraPerCacheQuery = 0.0001  // Redis/PG overhead
)

// Query handles the full multi-tier caching logic for the legacy /cache/query endpoint.
// The proxy endpoints use CheckCache + their own backend call + PersistAsync instead.
func (c *Coordinator) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	start := time.Now()

	// Step 0: Normalize (L0)
	normalized := c.normalizer.Normalize(req.Query)
	domain := req.Domain
	if domain == "" {
		domain = c.classifier.Classify(normalized)
	}

	// Step 1-3: Check all cache layers
	cacheKey := computeCacheKey(req, normalized)
	hit, err := c.CheckCache(ctx, req, normalized, cacheKey, domain, start)
	if err != nil {
		return nil, err
	}
	if hit != nil {
		return hit, nil
	}

	// Step 4: Backend Call (with singleflight and circuit breaker)
	// For legacy queries, compute embedding for L2b write-through.
	var emb []float32
	if c.embeddings != nil {
		emb, _ = c.embeddings.Embed(ctx, embeddableText(req, normalized))
	}

	res, err := c.fetchFromBackend(ctx, req.TenantID, normalized, domain, req.Query, emb)
	if err != nil {
		if c.metrics != nil {
			c.metrics.CacheMisses.WithLabelValues(req.TenantID).Inc()
		}
		return nil, err
	}

	c.logDecision(req, domain, normalized, audit.DecisionBackend, "", 0)
	if c.metrics != nil {
		c.metrics.CacheMisses.WithLabelValues(req.TenantID).Inc()
		c.metrics.BackendCalls.WithLabelValues(domain, req.TenantID).Inc()
		c.metrics.BackendCostTotal.Add(CostBackendPerRequest)
	}

	// Write-through: persist using legacy provider tag
	entry := &CacheEntry{
		TenantID:        req.TenantID,
		QueryRaw:        req.Query,
		QueryNormalized: normalized,
		QueryHash:       cacheKey,
		QueryDomain:     domain,
		Answer:          res.Answer,
		Provider:        "legacy",
		Model:           res.Model,
		TTLSeconds:      86400,
	}
	go c.PersistAsync(context.Background(), req.TenantID, normalized, cacheKey, domain, entry, emb)

	return &QueryResponse{
		Answer:    res.Answer,
		Source:    "backend",
		Hit:       false,
		LatencyMS: time.Since(start).Milliseconds(),
	}, nil
}

// CheckCache runs the L0→L1→L2a→L2b lookup chain and returns a cache hit response
// or nil if no hit is found. It is called by both Query (legacy) and proxy handlers.
//
//   - normalized: L0-normalised query text (or empty string for proxy requests without a single query field)
//   - cacheKey:   output of computeCacheKey(req, normalized)
//   - domain:     already-classified domain string
//   - start:      time.Now() captured by the caller for accurate latency
func (c *Coordinator) CheckCache(
	ctx context.Context,
	req QueryRequest,
	normalized, cacheKey, domain string,
	start time.Time,
) (*QueryResponse, error) {
	// Step 1: L1 Check (Exact Match in memory)
	if c.l1 != nil {
		if ans, ok := c.l1.Get(req.TenantID, cacheKey); ok {
			c.logDecision(req, domain, normalized, audit.DecisionL1Hit, "", 0)
			if c.metrics != nil {
				c.metrics.CacheHits.WithLabelValues("L1", req.TenantID).Inc()
				c.metrics.CacheLatency.WithLabelValues("L1").Observe(time.Since(start).Seconds())
				c.metrics.CacheCostSavedTotal.Add(CostBackendPerRequest - CostInfraPerCacheQuery)
			}
			return &QueryResponse{
				Answer:    ans,
				Source:    "L1",
				Hit:       true,
				LatencyMS: time.Since(start).Milliseconds(),
			}, nil
		}
	}

	// Step 2: L2a Check (Redis Exact Match)
	if c.l2a != nil {
		ans, err := c.l2a.Get(ctx, req.TenantID, cacheKey)
		if err == nil && ans != "" {
			// Backfill L1 if available
			if c.l1 != nil {
				c.l1.Set(req.TenantID, cacheKey, ans, 1*time.Hour)
			}
			c.logDecision(req, domain, normalized, audit.DecisionL2aHit, "", 0)
			if c.metrics != nil {
				c.metrics.CacheHits.WithLabelValues("L2a", req.TenantID).Inc()
				c.metrics.CacheLatency.WithLabelValues("L2a").Observe(time.Since(start).Seconds())
				c.metrics.CacheCostSavedTotal.Add(CostBackendPerRequest - CostInfraPerCacheQuery)
			}
			return &QueryResponse{
				Answer:    ans,
				Source:    "L2a",
				Hit:       true,
				LatencyMS: time.Since(start).Milliseconds(),
			}, nil
		}
	}

	// Step 3: L2b Check (Semantic Match in Postgres)
	if c.embeddings != nil && c.l2b != nil {
		textToEmbed := embeddableText(req, normalized)
		emb, err := c.embeddings.Embed(ctx, textToEmbed)
		if err == nil {
			candidates, err := c.l2b.Search(ctx, req.TenantID, emb, 5)
			if err == nil && len(candidates) > 0 {
				p := c.policy.GetPolicy(domain)

				for _, candle := range candidates {
					// Temporal check
					if policy.TemporalClass(normalized) != policy.TemporalClass(candle.QueryNormalized) {
						continue
					}

					confidence := policy.CalculateConfidence(candle.Similarity, candle.AgeSeconds(), p)
					if confidence >= p.ConfidenceThreshold {
						// Semantic Hit — backfill L1/L2a using the cacheKey of the current request.
						c.backfillKeyed(ctx, req.TenantID, cacheKey, candle.Answer)
						c.logDecision(req, domain, normalized, audit.DecisionL2bAccept, "", confidence)

						if c.metrics != nil {
							c.metrics.CacheHits.WithLabelValues("L2b", req.TenantID).Inc()
							c.metrics.ConfidenceScore.Observe(float64(confidence))
							c.metrics.CacheLatency.WithLabelValues("L2b").Observe(time.Since(start).Seconds())
							c.metrics.CacheCostSavedTotal.Add(CostBackendPerRequest - (CostEmbeddingPerQuery + CostInfraPerCacheQuery))
						}

						// Replay stored usage; mark estimated if tokens were zero (legacy entry).
						usage := backend.Usage{
							PromptTokens:     candle.PromptTokens,
							CompletionTokens: candle.CompletionTokens,
							Estimated:        candle.PromptTokens == 0 && candle.CompletionTokens == 0,
						}

						return &QueryResponse{
							Answer:         candle.Answer,
							Source:         "L2b",
							Hit:            true,
							Confidence:     confidence,
							LatencyMS:      time.Since(start).Milliseconds(),
							Usage:          usage,
							UsageFromCache: true,
						}, nil
					} else {
						if c.metrics != nil {
							c.metrics.PolicyRejections.WithLabelValues("low_confidence", domain).Inc()
						}
					}
				}
			}
		}
	}

	return nil, nil
}

// PersistAsync writes the given answer asynchronously to all configured cache tiers.
// It is called by both the legacy Query() path and the proxy handlers after a successful
// upstream response. The entry argument must have all fields populated by the caller.
func (c *Coordinator) PersistAsync(ctx context.Context, tenantID, normalized, cacheKey, domain string, entry *CacheEntry, embedding []float32) {
	// 1. L1
	if c.l1 != nil {
		c.l1.Set(tenantID, cacheKey, entry.Answer, 1*time.Hour)
	}

	// 2. L2a
	if c.l2a != nil {
		_ = c.l2a.Set(ctx, tenantID, cacheKey, entry.Answer, 24*time.Hour)
	}

	// 3. L2b
	if c.l2b != nil && len(embedding) > 0 {
		_ = c.l2b.Write(ctx, entry, embedding)
	}
}

func (c *Coordinator) logDecision(req QueryRequest, domain, normalized, decision, reason string, confidence float32) {
	if c.audit == nil {
		return
	}
	c.audit.Log(audit.LogEvent{
		RequestID:  req.RequestID,
		TenantID:   req.TenantID,
		QueryHash:  audit.HashQuery(normalized),
		Domain:     domain,
		Decision:   decision,
		Reason:     reason,
		Confidence: confidence,
	})
}

func (c *Coordinator) fetchFromBackend(ctx context.Context, tenantID, normalized, domain, original string, embedding []float32) (*backend.Response, error) {
	// Deduplicate concurrent requests for the same exact normalized query
	key := fmt.Sprintf("%s:%s", tenantID, normalized)

	val, err, _ := c.sfGroup.Do(key, func() (interface{}, error) {
		// Call backend through circuit breaker if available
		var resp *backend.Response
		var err error

		if c.breaker != nil {
			cbErr := c.breaker.Execute(func() error {
				resp, err = c.backend.Query(ctx, original)
				return err
			})
			if cbErr != nil {
				return nil, cbErr
			}
		} else {
			resp, err = c.backend.Query(ctx, original)
			if err != nil {
				return nil, err
			}
		}

		return resp, nil
	})

	if err != nil {
		return nil, err
	}

	return val.(*backend.Response), nil
}

// backfillKeyed writes answer to L1 and L2a using an opaque cacheKey (not the raw normalized string).
func (c *Coordinator) backfillKeyed(ctx context.Context, tenantID, cacheKey, answer string) {
	if c.l1 != nil {
		c.l1.Set(tenantID, cacheKey, answer, 1*time.Hour)
	}
	if c.l2a != nil {
		go func() {
			_ = c.l2a.Set(context.Background(), tenantID, cacheKey, answer, 24*time.Hour)
		}()
	}
}

// ReloadPolicies triggers a reload of the policy configuration file.
func (c *Coordinator) ReloadPolicies() error {
	if c.policy == nil {
		return fmt.Errorf("policy engine not initialized")
	}
	return c.policy.Reload()
}

// CheckHealth performs a deep health check of all dependencies.
func (c *Coordinator) CheckHealth(ctx context.Context) (map[string]interface{}, bool) {
	status := map[string]interface{}{
		"status":   "ready",
		"services": make(map[string]interface{}),
	}
	ready := true

	services := status["services"].(map[string]interface{})

	// 1. Redis check
	if c.l2a != nil && c.l2a.Client != nil {
		start := time.Now()
		err := c.l2a.Client.Ping(ctx).Err()
		services["redis"] = map[string]interface{}{
			"ok":         err == nil,
			"latency_ms": time.Since(start).Milliseconds(),
		}
		if err != nil {
			ready = false
		}
	}

	// 2. Postgres check
	if c.l2b != nil && c.l2b.pool != nil {
		start := time.Now()
		err := c.l2b.pool.Ping(ctx)
		services["postgres"] = map[string]interface{}{
			"ok":         err == nil,
			"latency_ms": time.Since(start).Milliseconds(),
		}
		if err != nil {
			ready = false
		}
	}

	// 3. Ollama check
	if c.embeddings != nil {
		start := time.Now()
		ok := c.embeddings.IsHealthy(ctx)
		services["ollama"] = map[string]interface{}{
			"ok":         ok,
			"latency_ms": time.Since(start).Milliseconds(),
		}
		if !ok {
			ready = false
		}
	}

	if !ready {
		status["status"] = "not_ready"
	}

	return status, ready
}

// ── Exported helpers for the api package ─────────────────────────────────────
// These wrap private functions so that proxy handlers can call them without
// duplicating the logic or creating a circular import.

// ComputeCacheKeyExported is the exported wrapper around computeCacheKey.
// It is used by proxy handlers in the api package to generate the stable cache key
// before calling CheckCache / PersistAsync.
func ComputeCacheKeyExported(req QueryRequest, normalized string) string {
	return computeCacheKey(req, normalized)
}

// EmbeddableTextExported is the exported wrapper around embeddableText.
// It is used by proxy handlers to get the text slice to embed for L2b search.
func EmbeddableTextExported(req QueryRequest, normalized string) string {
	return embeddableText(req, normalized)
}

// ClassifyDomain returns the domain classification for the given text.
// Used by proxy handlers to set the domain on cache entries.
func (c *Coordinator) ClassifyDomain(text string) string {
	if c.classifier == nil {
		return "general"
	}
	return c.classifier.Classify(text)
}

// EmbedText generates an embedding vector for the given text.
// Returns nil if the embeddings client is not configured or if embedding fails.
func (c *Coordinator) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if c.embeddings == nil {
		return nil, nil
	}
	return c.embeddings.Embed(ctx, text)
}

// CallOpenAICompatible forwards a ProxyRequest to the appropriate OpenAI-compatible
// upstream backend (openai, groq, together). The backend is constructed per-request
// since each request carries its own API key.
func (c *Coordinator) CallOpenAICompatible(ctx context.Context, req *backend.ProxyRequest) (*backend.ProxyResponse, error) {
	b := backend.NewOpenAICompatibleBackend(req.Provider)
	var resp *backend.ProxyResponse
	var err error

	if c.breaker != nil {
		cbErr := c.breaker.Execute(func() error {
			resp, err = b.ProxyCall(ctx, req)
			return err
		})
		if cbErr != nil {
			return nil, cbErr
		}
	} else {
		resp, err = b.ProxyCall(ctx, req)
	}
	return resp, err
}

// CallAnthropic forwards a ProxyRequest to the Anthropic Messages API.
func (c *Coordinator) CallAnthropic(ctx context.Context, req *backend.ProxyRequest) (*backend.ProxyResponse, error) {
	b := backend.NewAnthropicBackend()
	var resp *backend.ProxyResponse
	var err error

	if c.breaker != nil {
		cbErr := c.breaker.Execute(func() error {
			resp, err = b.ProxyCall(ctx, req)
			return err
		})
		if cbErr != nil {
			return nil, cbErr
		}
	} else {
		resp, err = b.ProxyCall(ctx, req)
	}
	return resp, err
}
