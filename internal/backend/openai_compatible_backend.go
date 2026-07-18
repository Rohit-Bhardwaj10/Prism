package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Well-known base URLs for OpenAI-compatible providers.
// These are used when no override is supplied via context / env.
var openAICompatibleBaseURLs = map[string]string{
	"openai":   "https://api.openai.com/v1/chat/completions",
	"groq":     "https://api.groq.com/openai/v1/chat/completions",
	"together": "https://api.together.xyz/v1/chat/completions",
}

// OpenAICompatibleBackend calls any provider that speaks the OpenAI Chat Completions wire format.
// This covers: OpenAI, Groq, Together AI, and most self-hosted models (vLLM, Ollama in OpenAI mode).
type OpenAICompatibleBackend struct {
	provider   string
	defaultURL string
	httpClient *http.Client
}

// NewOpenAICompatibleBackend constructs a backend for the given provider name.
// provider must be one of "openai", "groq", "together", or any future OpenAI-compatible service.
func NewOpenAICompatibleBackend(provider string) *OpenAICompatibleBackend {
	defaultURL := openAICompatibleBaseURLs[provider]
	if defaultURL == "" {
		// Unknown provider — caller must supply a BaseURL via context.
		defaultURL = ""
	}
	return &OpenAICompatibleBackend{
		provider:   provider,
		defaultURL: defaultURL,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// ── Wire format structs ──────────────────────────────────────────────────────

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// ── ProxyCall ────────────────────────────────────────────────────────────────

// ProxyCall forwards a ProxyRequest to the upstream provider and returns a ProxyResponse.
// The API key and optional base-URL override are read from req directly (not from context)
// so that the caller has full control over credential routing.
func (b *OpenAICompatibleBackend) ProxyCall(ctx context.Context, req *ProxyRequest) (*ProxyResponse, error) {
	start := time.Now()

	// Resolve target URL: context override > explicit field > per-provider default.
	targetURL := b.defaultURL
	if u := GetBaseURL(ctx); u != "" {
		targetURL = u
	}
	if req.BaseURL != "" {
		targetURL = req.BaseURL
	}
	if targetURL == "" {
		return nil, fmt.Errorf("openai_compatible_backend (%s): no base URL configured", b.provider)
	}

	// Build OpenAI message list (system prompt → user/assistant turns).
	msgs := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, openAIMessage{Role: m.Role, Content: m.Content})
	}

	payload, err := json.Marshal(openAIRequest{
		Model:    req.Model,
		Messages: msgs,
	})
	if err != nil {
		return nil, fmt.Errorf("openai_compatible_backend: marshal error: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("openai_compatible_backend: request build error: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}

	httpResp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai_compatible_backend (%s): http error: %w", b.provider, err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai_compatible_backend: read body error: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai_compatible_backend (%s): upstream returned %d: %s",
			b.provider, httpResp.StatusCode, truncate(string(body), 256))
	}

	var oaiResp openAIResponse
	if err := json.Unmarshal(body, &oaiResp); err != nil {
		return nil, fmt.Errorf("openai_compatible_backend: decode error: %w", err)
	}
	if oaiResp.Error != nil {
		return nil, fmt.Errorf("openai_compatible_backend (%s): provider error [%s]: %s",
			b.provider, oaiResp.Error.Code, oaiResp.Error.Message)
	}
	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("openai_compatible_backend (%s): no choices in response", b.provider)
	}

	content := oaiResp.Choices[0].Message.Content
	model := oaiResp.Model
	if model == "" {
		model = req.Model
	}

	return &ProxyResponse{
		Content: content,
		Model:   model,
		Usage: Usage{
			PromptTokens:     oaiResp.Usage.PromptTokens,
			CompletionTokens: oaiResp.Usage.CompletionTokens,
		},
		Source:    "backend",
		Hit:       false,
		LatencyMS: time.Since(start).Milliseconds(),
	}, nil
}

// truncate returns at most n runes of s, appending "…" when clipped.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
