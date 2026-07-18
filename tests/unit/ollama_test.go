package unit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Rohit-Bhardwaj10/prism/pkg/embeddings"
)

type mockBreaker struct {
	executed bool
	err      error
}

func (m *mockBreaker) Execute(fn func() error) error {
	m.executed = true
	if m.err != nil {
		return m.err
	}
	return fn()
}

func TestOllamaClient(t *testing.T) {
	// Create mock server
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify endpoint
		if r.URL.Path != "/api/embeddings" {
			t.Errorf("expected path /api/embeddings, got %s", r.URL.Path)
		}

		// Verify request content
		var reqBody embeddings.EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatal(err)
		}
		if reqBody.Prompt != "test query" {
			t.Errorf("expected 'test query', got %s", reqBody.Prompt)
		}

		// Send mock response
		resp := embeddings.EmbeddingResponse{
			Embedding: []float32{0.1, 0.2, 0.3},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer testServer.Close()

	breaker := &mockBreaker{}
	client := embeddings.NewOllamaClient(testServer.URL, "test-model", nil, breaker)
	emb, err := client.Embed(context.Background(), "test query")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !breaker.executed {
		t.Error("expected breaker to be executed")
	}
	if len(emb) != 3 || emb[0] != 0.1 {
		t.Errorf("unexpected embedding result: %v", emb)
	}
}

func TestOllamaClient_CircuitBreakerOpen(t *testing.T) {
	breaker := &mockBreaker{err: errors.New("cb open")}
	client := embeddings.NewOllamaClient("http://localhost", "test-model", nil, breaker)
	
	_, err := client.Embed(context.Background(), "test query")
	if err == nil || err.Error() != "cb open" {
		t.Errorf("expected 'cb open' error, got %v", err)
	}
}
