package api

import "fmt"

const (
	MaxQueryLength = 2048 // 2KB max for a single query to prevent DoS
)

// QueryRequest represents an incoming user query via POST /cache/query.
type QueryRequest struct {
	Query  string `json:"query"`
	Domain string `json:"domain,omitempty"` // optional - auto-classified if omitted
}

func (r *QueryRequest) Validate() error {
	if r.Query == "" {
		return fmt.Errorf("query cannot be empty")
	}
	if len(r.Query) > MaxQueryLength {
		return fmt.Errorf("query too long (max %d chars)", MaxQueryLength)
	}
	return nil
}

// QueryResponse is the unified response for both cache hits and misses.
type QueryResponse struct {
	Answer     string  `json:"answer"`
	Source     string  `json:"source"` // "L1", "L2a", "L2b", or "backend"
	Hit        bool    `json:"hit"`
	Confidence float32 `json:"confidence,omitempty"`
	LatencyMS  int64   `json:"latency_ms"`

	// Cache hit metadata
	CachedQuery string  `json:"cached_query,omitempty"`
	Similarity  float32 `json:"similarity,omitempty"`
	AgeSeconds  int     `json:"age_seconds,omitempty"`

	// Cache miss metadata
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// HealthResponse represents the status of the service and its dependencies.
type HealthResponse struct {
	Status   string                   `json:"status"`
	Services map[string]ServiceStatus `json:"services,omitempty"`
}

type ServiceStatus struct {
	OK        bool  `json:"ok"`
	LatencyMS int64 `json:"latency_ms,omitempty"`
}

// ── Proxy API models ──────────────────────────────────────────────────────────

// ProxyMessage is the wire-format message for proxy endpoints (OpenAI and Anthropic style).
type ProxyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIProxyRequest is the body accepted by POST /proxy/openai, /proxy/groq, /proxy/together.
// It is a subset of the OpenAI Chat Completions request — only fields we inspect or forward.
type OpenAIProxyRequest struct {
	Model    string         `json:"model"`
	System   string         `json:"system,omitempty"` // non-standard convenience field (moved to messages[] if set)
	Messages []ProxyMessage `json:"messages"`
}

func (r *OpenAIProxyRequest) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("model is required")
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("messages must not be empty")
	}
	return nil
}

// OpenAIProxyResponse mimics the OpenAI Chat Completions response shape so that
// clients using the OpenAI SDK can point at our proxy with zero changes.
type OpenAIProxyResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int  `json:"prompt_tokens"`
		CompletionTokens int  `json:"completion_tokens"`
		TotalTokens      int  `json:"total_tokens"`
		Estimated        bool `json:"usage_estimated,omitempty"`
	} `json:"usage"`
	// CacheMetadata is a non-standard extension header-level field surfaced in the body for observability.
	CacheMetadata struct {
		Hit        bool    `json:"hit"`
		Source     string  `json:"source"`
		Confidence float32 `json:"confidence,omitempty"`
		LatencyMS  int64   `json:"latency_ms"`
	} `json:"x_cache_metadata"`
}

// AnthropicProxyRequest is the body accepted by POST /proxy/anthropic.
type AnthropicProxyRequest struct {
	Model     string         `json:"model"`
	System    string         `json:"system,omitempty"`
	MaxTokens int            `json:"max_tokens,omitempty"` // forwarded verbatim but not cached
	Messages  []ProxyMessage `json:"messages"`
}

func (r *AnthropicProxyRequest) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("model is required")
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("messages must not be empty")
	}
	return nil
}

// AnthropicProxyResponse mimics the Anthropic Messages API response shape.
type AnthropicProxyResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int  `json:"input_tokens"`
		OutputTokens int  `json:"output_tokens"`
		Estimated    bool `json:"usage_estimated,omitempty"`
	} `json:"usage"`
	CacheMetadata struct {
		Hit        bool    `json:"hit"`
		Source     string  `json:"source"`
		Confidence float32 `json:"confidence,omitempty"`
		LatencyMS  int64   `json:"latency_ms"`
	} `json:"x_cache_metadata"`
}

