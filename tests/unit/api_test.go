package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rohit-Bhardwaj10/prism/internal/api"
	"github.com/Rohit-Bhardwaj10/prism/internal/audit"
	"github.com/Rohit-Bhardwaj10/prism/internal/backend"
	"github.com/Rohit-Bhardwaj10/prism/internal/cache"
	"github.com/Rohit-Bhardwaj10/prism/internal/policy"
	"github.com/golang-jwt/jwt/v5"
)

func TestAPI_Query(t *testing.T) {
	// Setup Mocks
	l1 := cache.NewL1Cache(1024)
	cacheKey := cache.ComputeCacheKeyExported(cache.QueryRequest{Query: "hello", TenantID: "default"}, "hello")
	l1.Set("default", cacheKey, "world", 0)

	coord := cache.NewCoordinator(cache.Config{
		L1:         l1,
		Normalizer: cache.NewNormalizer(),
		Classifier: policy.NewDomainClassifier(),
	})

	h := api.NewHandler(coord)

	// 1. Valid Cache Hit
	reqBody, _ := json.Marshal(api.QueryRequest{Query: "hello"})
	r := httptest.NewRequest("POST", "/cache/query", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	h.HandleQuery(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	var resp api.QueryResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if resp.Answer != "world" || resp.Source != "L1" {
		t.Errorf("Unexpected response: %+v", resp)
	}

	// 2. Invalid Method
	r = httptest.NewRequest("GET", "/cache/query", nil)
	w = httptest.NewRecorder()
	h.HandleQuery(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405, got %d", w.Code)
	}
}

func TestAPI_Health(t *testing.T) {
	h := api.NewHandler(nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/health", nil)

	h.HandleHealth(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	var resp api.HealthResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "ready" {
		t.Errorf("Expected ready, got %s", resp.Status)
	}
}

func TestAPI_TenantContext(t *testing.T) {
	l1 := cache.NewL1Cache(1024)
	mockBackend := &mockBackend{answer: "backend answer"}
	
	coord := cache.NewCoordinator(cache.Config{
		L1:         l1,
		Normalizer: cache.NewNormalizer(),
		Classifier: policy.NewDomainClassifier(),
		Backend:    mockBackend,
	})
	
	// Create a token for tenant1
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, api.Claims{
		TenantID: "tenant1",
	})
	tokenStr, _ := token.SignedString([]byte("dev-secret-change-in-prod"))

	// Initialize the server with the coordinator
	srv := api.NewServer(":8080", coord)

	// Since NewServer returns a *api.Server which has internal httpServer,
	// and we want to test the handler/middleware stack, we'll use the final handler
	// but api.Server doesn't export the mux or the finalHandler easily.
	// Oh wait, I can just use srv.ServeHTTP if I implement it or just use httptest.Server
	
	// Let's modify NewServer to make testing easier or just recreate parts.
	
	reqBody, _ := json.Marshal(api.QueryRequest{Query: "test"})
	r := httptest.NewRequest("POST", "/cache/query", bytes.NewBuffer(reqBody))
	r.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 with JWT, got %d", w.Code)
	}
}

type mockBackend struct {
	answer string
}

func (m *mockBackend) Query(ctx context.Context, query string) (*backend.Response, error) {
	return &backend.Response{
		Answer: m.answer,
		Model:  "mock-gpt",
	}, nil
}

func TestAPI_InputValidation(t *testing.T) {
	h := api.NewHandler(nil)
	
	// Case 1: Oversized query
	bigQuery := ""
	for i := 0; i < 3000; i++ {
		bigQuery += "a"
	}
	
	reqBody, _ := json.Marshal(api.QueryRequest{Query: bigQuery})
	r := httptest.NewRequest("POST", "/cache/query", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()
	
	h.HandleQuery(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for oversized query, got %d", w.Code)
	}

	// Case 2: Empty query
	reqBody, _ = json.Marshal(api.QueryRequest{Query: ""})
	r = httptest.NewRequest("POST", "/cache/query", bytes.NewBuffer(reqBody))
	w = httptest.NewRecorder()
	h.HandleQuery(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for empty query, got %d", w.Code)
	}
}

func TestAPI_Feedback(t *testing.T) {
	h := api.NewHandler(nil)
	fbBody := map[string]interface{}{
		"request_id": "req-123",
		"correct":    true,
	}
	data, _ := json.Marshal(fbBody)
	r := httptest.NewRequest("POST", "/feedback", bytes.NewBuffer(data))
	w := httptest.NewRecorder()
	
	h.HandleFeedback(w, r)
	if w.Code != http.StatusAccepted {
		t.Errorf("Expected 202, got %d", w.Code)
	}
}

func TestAPI_Streaming(t *testing.T) {
	l1 := cache.NewL1Cache(1024)
	cacheKey := cache.ComputeCacheKeyExported(cache.QueryRequest{Query: "hello", TenantID: "default"}, "hello")
	l1.Set("default", cacheKey, "world is beautiful", 0)
	
	coord := cache.NewCoordinator(cache.Config{
		L1:         l1,
		Normalizer: cache.NewNormalizer(),
		Classifier: policy.NewDomainClassifier(),
		Audit:      audit.NewLogger(),
	})
	srv := api.NewServer(":8080", coord)

	// Since NewServer handles the finalHandler, we use srv.ServeHTTP
	r := httptest.NewRequest("GET", "/cache/stream?q=hello", nil)
	w := httptest.NewRecorder()
	
	srv.ServeHTTP(w, r)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	
	got := w.Body.String()
	// The response should have "data: {"source":"L1","text":"world "}" etc
	// We check for some key parts
	if !strings.Contains(got, "\"source\":\"L1\"") {
		t.Errorf("SSE output missing expected source: %s", got)
	}
	if !strings.Contains(got, "event: done") {
		t.Errorf("SSE output missing done event: %s", got)
	}
}

func TestAPI_ProxyOpenAI_JSONAndStream(t *testing.T) {
	l1 := cache.NewL1Cache(1024)
	coordReq := cache.QueryRequest{
		Provider: "openai",
		Model:    "gpt-4o",
		Messages: []backend.Message{{Role: "user", Content: "What is Prism?"}},
		TenantID: "default",
	}
	cacheKey := cache.ComputeCacheKeyExported(coordReq, "What is Prism?")
	l1.Set("default", cacheKey, "Prism is an intelligent semantic cache.", 0)

	coord := cache.NewCoordinator(cache.Config{
		L1:         l1,
		Normalizer: cache.NewNormalizer(),
		Classifier: policy.NewDomainClassifier(),
		Audit:      audit.NewLogger(),
	})
	srv := api.NewServer(":8080", coord)

	// 1. Regular JSON proxy request
	reqBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"What is Prism?"}]}`
	r := httptest.NewRequest("POST", "/proxy/openai", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Cache") != "L1" {
		t.Errorf("Expected X-Cache header to be L1, got %s", w.Header().Get("X-Cache"))
	}
	var resp api.OpenAIProxyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse OpenAI proxy response: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Prism is an intelligent semantic cache." {
		t.Errorf("Unexpected choices content: %+v", resp.Choices)
	}

	// 2. Stream = true request
	reqBodyStream := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"What is Prism?"}]}`
	r = httptest.NewRequest("POST", "/proxy/openai", bytes.NewBufferString(reqBodyStream))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("Expected text/event-stream content type, got %s", w.Header().Get("Content-Type"))
	}
	streamOut := w.Body.String()
	if !strings.Contains(streamOut, "chat.completion.chunk") {
		t.Errorf("Expected chat.completion.chunk in stream output: %s", streamOut)
	}
	if !strings.Contains(streamOut, "data: [DONE]") {
		t.Errorf("Expected [DONE] in stream output: %s", streamOut)
	}
}

func TestAPI_ProxyAnthropic_JSONAndStream(t *testing.T) {
	l1 := cache.NewL1Cache(1024)
	coordReq := cache.QueryRequest{
		Provider: "anthropic",
		Model:    "claude-3-5-sonnet",
		Messages: []backend.Message{{Role: "user", Content: "Explain Go channels."}},
		TenantID: "default",
	}
	cacheKey := cache.ComputeCacheKeyExported(coordReq, "Explain Go channels.")
	l1.Set("default", cacheKey, "Channels are conduits for typed communication.", 0)

	coord := cache.NewCoordinator(cache.Config{
		L1:         l1,
		Normalizer: cache.NewNormalizer(),
		Classifier: policy.NewDomainClassifier(),
		Audit:      audit.NewLogger(),
	})
	srv := api.NewServer(":8080", coord)

	// 1. Regular JSON proxy request
	reqBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"Explain Go channels."}]}`
	r := httptest.NewRequest("POST", "/proxy/anthropic", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp api.AnthropicProxyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode Anthropic response: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "Channels are conduits for typed communication." {
		t.Errorf("Unexpected Anthropic content: %+v", resp.Content)
	}

	// 2. Streaming request
	reqBodyStream := `{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"Explain Go channels."}]}`
	r = httptest.NewRequest("POST", "/proxy/anthropic", bytes.NewBufferString(reqBodyStream))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}
	streamOut := w.Body.String()
	if !strings.Contains(streamOut, "event: message_start") {
		t.Errorf("Expected message_start event: %s", streamOut)
	}
	if !strings.Contains(streamOut, "event: content_block_delta") {
		t.Errorf("Expected content_block_delta event: %s", streamOut)
	}
	if !strings.Contains(streamOut, "event: message_stop") {
		t.Errorf("Expected message_stop event: %s", streamOut)
	}
}

func TestAPI_ProxyMessage_MultimodalUnmarshal(t *testing.T) {
	// 1. Plain string content
	msgJSON := `{"role":"user","content":"hello world"}`
	var msg api.ProxyMessage
	if err := json.Unmarshal([]byte(msgJSON), &msg); err != nil {
		t.Fatalf("Failed to unmarshal string message: %v", err)
	}
	if msg.Content != "hello world" {
		t.Errorf("Expected 'hello world', got %q", msg.Content)
	}

	// 2. Structured multimodal array of content parts
	multimodalJSON := `{"role":"user","content":[{"type":"text","text":"Analyze this code"},{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]}`
	var multiMsg api.ProxyMessage
	if err := json.Unmarshal([]byte(multimodalJSON), &multiMsg); err != nil {
		t.Fatalf("Failed to unmarshal multimodal message: %v", err)
	}
	if multiMsg.Content != "Analyze this code" {
		t.Errorf("Expected 'Analyze this code', got %q", multiMsg.Content)
	}
}

func TestCoordinator_HyperparameterCacheSeparation(t *testing.T) {
	tempZero := 0.0
	tempOne := 1.0

	reqA := cache.QueryRequest{
		Provider:    "openai",
		Model:       "gpt-4o",
		Messages:    []backend.Message{{Role: "user", Content: "code"}},
		Temperature: &tempZero,
	}
	reqB := cache.QueryRequest{
		Provider:    "openai",
		Model:       "gpt-4o",
		Messages:    []backend.Message{{Role: "user", Content: "code"}},
		Temperature: &tempOne,
	}

	keyA := cache.ComputeCacheKeyExported(reqA, "code")
	keyB := cache.ComputeCacheKeyExported(reqB, "code")

	if keyA == keyB {
		t.Errorf("Expected different cache keys for different temperatures, got identical key %s", keyA)
	}
}

