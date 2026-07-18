package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/api"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/audit"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/backend"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/cache"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/metrics"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/policy"
	"github.com/Rohit-Bhardwaj10/semantic-cache/internal/resilience"
	"github.com/Rohit-Bhardwaj10/semantic-cache/pkg/embeddings"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log.Println("--- Starting Semantic Cache Proxy ---")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Load Configurations (Environment Variables)
	redisAddr := getEnv("REDIS_URL", "localhost:6379")
	dbURL := getEnv("POSTGRES_URL", "postgres://cache:cache@localhost:5432/cache?sslmode=disable")
	ollamaURL := getEnv("OLLAMA_URL", "http://localhost:11434")
	backendURL := getEnv("BACKEND_URL", "http://mock-backend:8081")
	l1MaxBytesRaw := getEnv("L1_MAX_BYTES", "134217728") // 128MB
	l1MaxBytes, _ := strconv.ParseInt(l1MaxBytesRaw, 10, 64)

	// 2. Initialize Infrastructure
	// Postgres Pool
	pgPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to Postgres: %v", err)
	}
	defer pgPool.Close()

	// Redis client (L2a)
	l2a := cache.NewL2aCache(redisAddr, "", 0)
	defer l2a.Close()

	// 3. Initialize Domain Knowledge/Policy
	policyEngine, err := policy.NewEngine("configs/policies.yaml")
	if err != nil {
		log.Printf("Warning: Failed to load policies.yaml: %v. Using defaults.", err)
		// Sprint 1 might not have the file yet, we can create a dummy one or handle
	}
	policyEngine.WatchSIGHUP() // hot reloading

	classifier := policy.NewDomainClassifier()
	normalizer := cache.NewNormalizer()
	_ = normalizer.LoadSynonyms("configs/synonyms.yaml")

	// 4. Initialize Core Tiers
	l1 := cache.NewL1Cache(l1MaxBytes)
	l2b := cache.NewL2bCache(pgPool, "nomic-embed-text", "v1")

	storedVersions, err := l2b.GetStoredVersions(ctx)
	if err != nil {
		log.Printf("Warning: Failed to check stored embedding versions: %v", err)
	} else {
		for _, v := range storedVersions {
			if v.Model != "nomic-embed-text" || v.Version != "v1" {
				log.Fatalf("CRITICAL: Stored embedding version mismatch! Found (%s, %s), expected (nomic-embed-text, v1). "+
					"Please migrate or flush the cache to avoid corruption.", v.Model, v.Version)
			}
		}
	}

	auditLogger := audit.NewLogger()
	promMetrics := metrics.InitMetrics()
	
	breaker := resilience.NewCircuitBreaker(5, 30*time.Second)
	ollamaClient := embeddings.NewOllamaClient(ollamaURL, "nomic-embed-text", l2a.Client, breaker)
	
	backendClient := backend.NewHTTPClient(backendURL)

	// 5. Orchestrate with Coordinator
	coord := cache.NewCoordinator(cache.Config{
		Normalizer: normalizer,
		L1:         l1,
		L2a:        l2a,
		L2b:        l2b,
		Embeddings: ollamaClient,
		Policy:     policyEngine,
		Classifier: classifier,
		Backend:    backendClient,
		Breaker:    breaker,
		Audit:      auditLogger,
		Metrics:    promMetrics,
	})

	// 6. Start Lifecycle Tasks
	// Startup Warmup (Sprint 4)
	go func() {
		_ = coord.LoadWarmCache(context.Background(), 5000)
	}()
	
	// Distributed Invalidation Listener (Sprint 4)
	go coord.StartInvalidationListener(context.Background())

	// 7. Initialize & Start API Server (Sprint 5)
	srv := api.NewServer(":8080", coord)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	fmt.Println("Semantic Cache Proxy is ready on http://localhost:8080")
	fmt.Println("  Legacy:    POST /cache/query")
	fmt.Println("  Proxy:     POST /proxy/{openai,groq,together,anthropic}")
	fmt.Println("             Authorization: Bearer <provider-api-key>")
	fmt.Println("             X-Tenant-ID: <tenant>  (optional, defaults to 'default')")

	// 8. Graceful Shutdown
	<-ctx.Done()
	log.Println("Shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Graceful shutdown failed: %v", err)
	}

	log.Println("Server stopped.")
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
