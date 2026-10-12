// cmd/loadgen/main.go — Continuous traffic generator for the semantic cache
// Sends real HTTP requests to the running cache proxy so Grafana shows live data.
//
// Run: go run ./cmd/loadgen/
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const defaultURL = "http://localhost:8080/cache/query"

// A curated set of real-world queries across domains
var queries = []struct {
	q      string
	tenant string
}{
	// General knowledge
	{"What is the capital of France?", "tenant_acme"},
	{"Who invented the telephone?", "tenant_acme"},
	{"What is quantum computing?", "tenant_acme"},
	{"Who is Albert Einstein?", "tenant_acme"},
	{"What is the speed of light?", "tenant_acme"},
	{"Who wrote Hamlet?", "tenant_acme"},

	// Variants (will hit cache)
	{"what's the capital of france", "tenant_acme"},
	{"who invented telephone?", "tenant_acme"},
	{"explain quantum computing", "tenant_acme"},
	{"tell me about Albert Einstein", "tenant_acme"},
	{"how fast is light?", "tenant_acme"},
	{"who wrote the play hamlet", "tenant_acme"},

	// Coding
	{"How do I reverse a string in Python?", "tenant_beta"},
	{"What is machine learning?", "tenant_beta"},
	{"How does HTTP work?", "tenant_beta"},
	{"What is a neural network?", "tenant_beta"},
	{"How to sort a list in Python?", "tenant_beta"},
	{"What is Docker?", "tenant_beta"},
	{"What is Kubernetes?", "tenant_beta"},
	{"How does Redis work?", "tenant_beta"},
	{"What is Grafana used for?", "tenant_beta"},
	{"How does Prometheus work?", "tenant_beta"},

	// Coding variants
	{"python reverse string how?", "tenant_beta"},
	{"what's machine learning?", "tenant_beta"},
	{"how does the HTTP protocol work?", "tenant_beta"},
	{"explain neural networks", "tenant_beta"},
	{"sort list python", "tenant_beta"},

	// Second tenant doing same queries (isolation demo)
	{"What is machine learning?", "tenant_gamma"},
	{"How do I reverse a string in Python?", "tenant_gamma"},
	{"What is the capital of France?", "tenant_gamma"},
}

type queryReq struct {
	Query    string `json:"query"`
	TenantID string `json:"tenant_id"`
}

type queryResp struct {
	Answer string  `json:"answer"`
	Hit    bool    `json:"hit"`
	Source string  `json:"source"`
}

var (
	totalSent   int64
	totalHits   int64
	totalErrors int64
	mu          sync.Mutex
	tierCounts  = map[string]int64{}
)

func sendQuery(client *http.Client, baseURL, query, tenant string) {
	body, _ := json.Marshal(queryReq{Query: query, TenantID: tenant})
	req, _ := http.NewRequest("POST", baseURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Try with auth header too (JWT might be required)
	req.Header.Set("X-Tenant-ID", tenant)

	resp, err := client.Do(req)
	atomic.AddInt64(&totalSent, 1)
	if err != nil {
		atomic.AddInt64(&totalErrors, 1)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var r queryResp
		if err := json.NewDecoder(resp.Body).Decode(&r); err == nil {
			if r.Hit {
				atomic.AddInt64(&totalHits, 1)
			}
			if r.Source != "" {
				mu.Lock()
				tierCounts[r.Source]++
				mu.Unlock()
			}
		}
	}
}

func printStats() {
	sent := atomic.LoadInt64(&totalSent)
	hits := atomic.LoadInt64(&totalHits)
	errs := atomic.LoadInt64(&totalErrors)

	hitRate := 0.0
	if sent > 0 {
		hitRate = float64(hits) / float64(sent) * 100
	}

	mu.Lock()
	tc := make(map[string]int64)
	for k, v := range tierCounts {
		tc[k] = v
	}
	mu.Unlock()

	fmt.Printf("\r  Sent: %5d  Hits: %5d (%.1f%%)  Errors: %d  [L1:%d L2a:%d L2b:%d]   ",
		sent, hits, hitRate, errs,
		tc["L1"], tc["L2a"], tc["L2b"])
}

func main() {
	baseURL := defaultURL
	if u := os.Getenv("CACHE_URL"); u != "" {
		baseURL = u
	}
	workers := 20
	if w := os.Getenv("WORKERS"); w != "" {
		if n, err := strconv.Atoi(w); err == nil {
			workers = n
		}
	}

	log.Printf("🚀 Load generator starting — target: %s  workers: %d", baseURL, workers)
	log.Printf("   Sends a mix of fresh queries + variants to generate cache hit/miss traffic")
	log.Printf("   Press Ctrl+C to stop\n")

	client := &http.Client{Timeout: 5 * time.Second}

	// Stats printer
	go func() {
		for range time.Tick(500 * time.Millisecond) {
			printStats()
		}
	}()

	// Worker pool
	work := make(chan struct{ q, t string }, workers*4)

	for i := 0; i < workers; i++ {
		go func() {
			for job := range work {
				sendQuery(client, baseURL, job.q, job.t)
				time.Sleep(jitter(50*time.Millisecond, 200*time.Millisecond))
			}
		}()
	}

	// Feed queries in a loop — 70% repeats, 30% fresh, rate ≤ 50 req/s to stay under limit
	tick := time.NewTicker(20 * time.Millisecond) // 50/sec total
	defer tick.Stop()

	i := 0
	for range tick.C {
		var q, t string
		if rand.Float64() < 0.70 {
			// 70% — pick from known list (will hit cache on repeat)
			entry := queries[rand.Intn(len(queries))]
			q, t = entry.q, entry.tenant
		} else {
			// 30% — fresh unique query (will miss and go to backend)
			q = fmt.Sprintf("What is concept %d in computer science?", rand.Intn(50000))
			t = "tenant_acme"
		}
		work <- struct{ q, t string }{q, t}
		i++
	}
}

func jitter(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
}
