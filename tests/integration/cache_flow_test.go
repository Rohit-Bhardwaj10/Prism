package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rohit-Bhardwaj10/prism/internal/backend"
	"github.com/Rohit-Bhardwaj10/prism/internal/cache"
	"github.com/Rohit-Bhardwaj10/prism/internal/policy"
	"github.com/Rohit-Bhardwaj10/prism/internal/resilience"
	"github.com/Rohit-Bhardwaj10/prism/pkg/embeddings"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCoordinator_Integration(t *testing.T) {
	ctx := context.Background()

	// 1. Setup Dependencies
	// Mocks
	mockBackend := &mockBackend{answer: "backend answer"}
	
	// Mock Ollama (httptest)
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := embeddings.EmbeddingResponse{
			Embedding: []float32{0.1, 0.2, 0.3},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()
	
	ollamaClient := embeddings.NewOllamaClient(ollamaServer.URL, "test-model", nil, nil)

	// Real L1
	l1 := cache.NewL1Cache(1024 * 1024)

	// L2a/L2b (Real if containers are up, otherwise we might skip)
	// For this test, we'll try to connect but handle failures
	l2a := cache.NewL2aCache("localhost:6379", "", 0)
	defer l2a.Close()

	// Use pool from environment or default
	pool, err := pgxpool.New(ctx, "postgres://cache:cache@localhost:5432/cache?sslmode=disable")
	if err != nil {
		t.Skip("Skipping full integration test: Postgres not available")
		return
	}
	defer pool.Close()
	l2b := cache.NewL2bCache(pool, "test-model", "v1")

	// Global Logic
	normalizer := cache.NewNormalizer()
	// Dummy policy engine
	policyEngine, _ := policy.NewEngine("../../configs/policies.yaml")
	classifier := policy.NewDomainClassifier()
	breaker := resilience.NewCircuitBreaker(3, 10*time.Second)

	coord := cache.NewCoordinator(cache.Config{
		Normalizer: normalizer,
		L1:         l1,
		L2a:        l2a,
		L2b:        l2b,
		Embeddings: ollamaClient,
		Policy:     policyEngine,
		Classifier: classifier,
		Backend:    mockBackend,
		Breaker:    breaker,
	})

	// 2. Scenario 1: Fresh Miss (Backend Hit + Write-Through)
	req := cache.QueryRequest{
		Query:    "What is the capital of France?",
		TenantID: "test_tenant",
	}

	resp, err := coord.Query(ctx, req)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if resp.Source != "backend" {
		t.Errorf("Expected source backend, got %s", resp.Source)
	}

	// Wait a bit for async write-through
	time.Sleep(100 * time.Millisecond)

	// 3. Scenario 2: L1 Hit
	resp, err = coord.Query(ctx, req)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if resp.Source != "L1" || !resp.Hit {
		t.Errorf("Expected L1 hit, got %s (hit=%v)", resp.Source, resp.Hit)
	}

	// 4. Scenario 3: Semantic Hit (L2b)
	reqSimilar := cache.QueryRequest{
		Query:    "CAPITAL OF FRANCE?",
		TenantID: "test_tenant",
	}
	resp, err = coord.Query(ctx, reqSimilar)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	// This might be an L1 hit due to normalization or L2b if we changed the text slightly.
	if !resp.Hit {
		t.Errorf("Expected hit for similar query, got miss")
	}
}

type mockBackend struct {
	answer string
	calls  int
}

func (m *mockBackend) Query(ctx context.Context, query string) (*backend.Response, error) {
	m.calls++
	return &backend.Response{
		Answer: m.answer,
		Model:  "mock-gpt",
	}, nil
}
