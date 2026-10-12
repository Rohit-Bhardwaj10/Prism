package embeddings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type OpenAIClient struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	HTTPClient *http.Client
	Redis      *redis.Client
	Breaker    Breaker
}

type OpenAIEmbeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type OpenAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func NewOpenAIClient(baseURL, apiKey, model string, rdb *redis.Client, breaker Breaker) *OpenAIClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	// Make sure base URL doesn't have trailing slash
	baseURL = strings.TrimSuffix(baseURL, "/")

	dims := 0
	if val := os.Getenv("OPENAI_EMBEDDING_DIMENSIONS"); val != "" {
		fmt.Sscanf(val, "%d", &dims)
	}
	if dims <= 0 && strings.HasPrefix(model, "text-embedding-3") {
		dims = 768 // default to 768 to match PostgreSQL VECTOR(768)
	}

	return &OpenAIClient{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Model:      model,
		Dimensions: dims,
		Redis:      rdb,
		Breaker:    breaker,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *OpenAIClient) Embed(ctx context.Context, text string) ([]float32, error) {
	h := sha256.Sum256([]byte(text))
	cacheKey := fmt.Sprintf("emb:%s:%d:%x", c.Model, c.Dimensions, h)
	if c.Redis != nil {
		val, err := c.Redis.Get(ctx, cacheKey).Bytes()
		if err == nil {
			var emb []float32
			if err := json.Unmarshal(val, &emb); err == nil {
				return emb, nil
			}
		}
	}

	var result []float32
	embedFunc := func() error {
		reqBody := OpenAIEmbeddingRequest{
			Model:      c.Model,
			Input:      []string{text},
			Dimensions: c.Dimensions,
		}

		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrEmbedMarshal, err)
		}

		url := fmt.Sprintf("%s/embeddings", c.BaseURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(data))
		if err != nil {
			return fmt.Errorf("openai request creation failed: %w", err)
		}
		
		req.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("openai request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%w: status %d", ErrEmbedServer, resp.StatusCode)
		}

		var embedResp OpenAIEmbeddingResponse
		if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
			return fmt.Errorf("%w: %v", ErrEmbedDecode, err)
		}

		if len(embedResp.Data) == 0 {
			return fmt.Errorf("openai response contained no embedding data")
		}

		result = embedResp.Data[0].Embedding
		return nil
	}

	var err error
	if c.Breaker != nil {
		err = c.Breaker.Execute(embedFunc)
	} else {
		err = embedFunc()
	}

	if err != nil {
		return nil, err
	}

	if c.Redis != nil {
		data, _ := json.Marshal(result)
		c.Redis.Set(ctx, cacheKey, data, 7*24*time.Hour)
	}

	return result, nil
}

func (c *OpenAIClient) IsHealthy(ctx context.Context) bool {
	// A simple healthy check for OpenAI might just check the models endpoint or just return true 
	// to avoid spamming the API. We'll return true to assume health unless the breaker trips.
	return true
}
