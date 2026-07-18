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
	l1.Set("default", "hello", "world", 0)

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
	l1.Set("default", "hello", "world is beautiful", 0)
	
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
