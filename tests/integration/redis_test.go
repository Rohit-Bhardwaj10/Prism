package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Rohit-Bhardwaj10/prism/internal/cache"
)

func TestL2aRedisIntegration(t *testing.T) {
	// Attempt to connect to local redis or container
	// In CI/Dev, this should match docker-compose (redis:6379)
	l2a := cache.NewL2aCache("localhost:6379", "", 0)
	defer l2a.Close()

	ctx := context.Background()
	tenant := "test_tenant"
	query := "hello redis"
	value := "world"

	// Test Set
	err := l2a.Set(ctx, tenant, query, value, 10*time.Second)
	if err != nil {
		t.Skip("Skipping Redis integration test: No redis available on localhost:6379")
		return
	}

	// Test Get
	got, err := l2a.Get(ctx, tenant, query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != value {
		t.Errorf("expected %s, got %s", value, got)
	}

	// Test Miss
	got, err = l2a.Get(ctx, tenant, "unknown")
	if err != nil {
		t.Fatalf("unexpected error on miss: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string on miss, got %s", got)
	}
}
