package cache

import (
	"context"
	"testing"
	"time"

	"github.com/Rohit-Bhardwaj10/prism/internal/policy"
)

// BenchmarkCoordinator_L1_Hit measures the performance of an L1 cache hit.
// Target: < 1ms (P99 < 8ms)
func BenchmarkCoordinator_L1_Hit(b *testing.B) {
	ctx := context.Background()
	normalizer := NewNormalizer()
	l1 := NewL1Cache(1024 * 1024)

	coord := NewCoordinator(Config{
		Normalizer: normalizer,
		L1:         l1,
		Policy:     policy.NewDomainClassifier().engine(), // basic engine
	})

	tenant := "bench_tenant"
	query := "What is the capital of France?"
	ans := "Paris"

	// Pre-populate
	coord.l1.Set(tenant, coord.normalizer.Normalize(query), ans, 1*time.Hour)

	req := QueryRequest{
		Query:    query,
		TenantID: tenant,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = coord.Query(ctx, req)
	}
}

// BenchmarkCoordinator_L2b_Hit measures the performance of a semantic match.
// Target: ~10-15ms
func BenchmarkCoordinator_L2b_Hit(b *testing.B) {
	// This would require a mock database or a live one.
	// For now, we'll mark it as a placeholder for the canonical run config.
}
