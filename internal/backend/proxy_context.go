package backend

import "context"

// contextKey is the package-private key type for context values.
type contextKey string

const (
	// APIKeyContextKey carries the upstream provider API key through the request context.
	APIKeyContextKey contextKey = "provider_api_key"
	// BaseURLContextKey carries an optional override for the upstream provider base URL.
	BaseURLContextKey contextKey = "provider_base_url"
)

// Message represents a single turn in a multi-turn conversation (OpenAI / Anthropic style).
type Message struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"` // Text content of the message
}

// Usage holds token consumption reported by the upstream provider.
// Zero values are safe — old cache entries (from /cache/query) use zero usage
// and set UsageEstimated=true on replay.
type Usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	Estimated        bool `json:"usage_estimated,omitempty"` // true when replaying a legacy entry
}

// ProxyRequest is the canonical, provider-agnostic representation of a proxy call.
// Proxy handlers parse the provider-specific wire format into this struct and pass
// it to the coordinator via CheckCache / PersistAsync.
type ProxyRequest struct {
	Provider  string    // "openai" | "anthropic" | "groq" | "together"
	Model     string    // e.g. "gpt-4o", "claude-3-5-sonnet-20241022"
	System    string    // system prompt (may be empty)
	Messages  []Message // ordered conversation turns
	TenantID  string    // injected by AuthMiddleware
	RequestID string    // injected by RequestIDMiddleware
	Domain    string    // optional hint; auto-classified if empty
	APIKey    string    // upstream provider API key (from Authorization header)
	BaseURL   string    // override base URL (empty = use well-known default)
}

// ProxyResponse is the canonical response returned by proxy handlers.
// The HTTP handler serialises this back to the client in its native format.
type ProxyResponse struct {
	Content string // full assistant text
	Model   string // model as reported by provider
	Usage   Usage
	// Source is "L1", "L2a", "L2b", or "backend" (set by coordinator / handler).
	Source string
	// Hit is true when the response came from any cache layer.
	Hit bool
	// Confidence is non-zero only for L2b semantic hits.
	Confidence float32
	// LatencyMS is wall-clock latency measured by the handler.
	LatencyMS int64
}

// WithAPIKey returns a derived context carrying the given API key.
func WithAPIKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, APIKeyContextKey, key)
}

// GetAPIKey retrieves the API key from ctx. Returns "" if not set.
func GetAPIKey(ctx context.Context) string {
	v, _ := ctx.Value(APIKeyContextKey).(string)
	return v
}

// WithBaseURL returns a derived context carrying an upstream base-URL override.
func WithBaseURL(ctx context.Context, url string) context.Context {
	return context.WithValue(ctx, BaseURLContextKey, url)
}

// GetBaseURL retrieves the base URL override from ctx. Returns "" if not set.
func GetBaseURL(ctx context.Context) string {
	v, _ := ctx.Value(BaseURLContextKey).(string)
	return v
}
