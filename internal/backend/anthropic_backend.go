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

const (
	anthropicDefaultURL = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion = "2023-06-01"
)

// AnthropicBackend calls the Anthropic Messages API.
// Reference: https://docs.anthropic.com/en/api/messages
type AnthropicBackend struct {
	httpClient *http.Client
}

// NewAnthropicBackend constructs an AnthropicBackend with sensible HTTP timeouts.
func NewAnthropicBackend() *AnthropicBackend {
	return &AnthropicBackend{
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// ── Wire format structs ──────────────────────────────────────────────────────

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ── ProxyCall ────────────────────────────────────────────────────────────────

// ProxyCall forwards a ProxyRequest to the Anthropic Messages API.
func (b *AnthropicBackend) ProxyCall(ctx context.Context, req *ProxyRequest) (*ProxyResponse, error) {
	start := time.Now()

	targetURL := anthropicDefaultURL
	if u := GetBaseURL(ctx); u != "" {
		targetURL = u
	}
	if req.BaseURL != "" {
		targetURL = req.BaseURL
	}

	// Anthropic separates system prompt from the message list.
	// Only "user" and "assistant" roles are allowed in messages[].
	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			// Anthropic rejects system turns in messages[] — skip them here;
			// the caller should have put system content in req.System.
			continue
		}
		msgs = append(msgs, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	// Default to a sensible max_tokens if caller doesn't override (proxy doesn't expose it yet).
	payload, err := json.Marshal(anthropicRequest{
		Model:     req.Model,
		MaxTokens: 4096,
		System:    req.System,
		Messages:  msgs,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic_backend: marshal error: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("anthropic_backend: request build error: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
	if req.APIKey != "" {
		httpReq.Header.Set("x-api-key", req.APIKey)
	}

	httpResp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic_backend: http error: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic_backend: read body error: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic_backend: upstream returned %d: %s",
			httpResp.StatusCode, truncate(string(body), 256))
	}

	var aResp anthropicResponse
	if err := json.Unmarshal(body, &aResp); err != nil {
		return nil, fmt.Errorf("anthropic_backend: decode error: %w", err)
	}
	if aResp.Error != nil {
		return nil, fmt.Errorf("anthropic_backend: provider error [%s]: %s",
			aResp.Error.Type, aResp.Error.Message)
	}

	// Extract text from the first content block of type "text".
	var content string
	for _, block := range aResp.Content {
		if block.Type == "text" {
			content = block.Text
			break
		}
	}
	if content == "" {
		return nil, fmt.Errorf("anthropic_backend: no text content in response")
	}

	model := aResp.Model
	if model == "" {
		model = req.Model
	}

	return &ProxyResponse{
		Content: content,
		Model:   model,
		Usage: Usage{
			PromptTokens:     aResp.Usage.InputTokens,
			CompletionTokens: aResp.Usage.OutputTokens,
		},
		Source:    "backend",
		Hit:       false,
		LatencyMS: time.Since(start).Milliseconds(),
	}, nil
}
