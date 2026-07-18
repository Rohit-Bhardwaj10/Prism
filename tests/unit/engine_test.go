package unit

import (
	"os"
	"testing"
	"github.com/Rohit-Bhardwaj10/prism/internal/policy"
)

func TestPolicyEngine(t *testing.T) {
	content := `
weather:
  min_similarity: 0.88
  max_staleness_seconds: 1800
  confidence_threshold: 0.72
  sim_weight: 0.40
  fresh_weight: 0.60

general:
  min_similarity: 0.85
  max_staleness_seconds: 3600
  confidence_threshold: 0.70
  sim_weight: 0.50
  fresh_weight: 0.50
`
	tmpfile, err := os.CreateTemp("", "policies*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	engine, err := policy.NewEngine(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Test GetPolicy for existing domain
	p := engine.GetPolicy("weather")
	if p.MinSimilarity != 0.88 {
		t.Errorf("expected 0.88, got %v", p.MinSimilarity)
	}

	// Test fallback to general
	p = engine.GetPolicy("unknown")
	if p.MinSimilarity != 0.85 {
		t.Errorf("expected 0.85, got %v", p.MinSimilarity)
	}
}
