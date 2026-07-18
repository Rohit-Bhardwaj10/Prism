package unit

import (
	"testing"
	"github.com/Rohit-Bhardwaj10/prism/internal/policy"
)

func TestCalculateConfidence(t *testing.T) {
	p := policy.Policy{
		MinSimilarity:       0.85,
		MaxStalenessSeconds: 3600, // 1 hour
		SimWeight:           0.5,
		FreshWeight:         0.5,
		ConfidenceThreshold: 0.7,
	}

	tests := []struct {
		name       string
		sim        float32
		ageSeconds int
		wantMin    float32 // we check if it's roughly correct
	}{
		{
			name:       "Below hard gate",
			sim:        0.80,
			ageSeconds: 0,
			wantMin:    0,
		},
		{
			name:       "Perfect match, zero age",
			sim:        1.0,
			ageSeconds: 0,
			wantMin:    1.0, // 0.5*1.0 + 0.5*1.0
		},
		{
			name:       "Borderline match, 1 hour age",
			sim:        0.9,
			ageSeconds: 3600,
			// freshness = exp(-3600/3600) = exp(-1) approx 0.367
			// additive = 0.5*0.9 + 0.5*0.367 = 0.45 + 0.1835 = 0.6335
			wantMin:    0.63,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.CalculateConfidence(tt.sim, tt.ageSeconds, p)
			if tt.wantMin == 0 {
				if got != 0 {
					t.Errorf("expected 0 for below hard gate, got %v", got)
				}
			} else {
				if got < tt.wantMin-0.01 || got > tt.wantMin+0.01 {
					t.Errorf("got %v, want approx %v", got, tt.wantMin)
				}
			}
		})
	}
}

func TestOldMultiplicativeCheck(t *testing.T) {
	// Additive vs Multiplicative distinction test
	p := policy.Policy{
		MinSimilarity:       0.8,
		MaxStalenessSeconds: 3600,
		SimWeight:           0.5,
		FreshWeight:         0.5,
	}
	
	sim := float32(0.9)
	age := 3600 // fresh = 0.367
	
	got := policy.CalculateConfidence(sim, age, p)
	multiplicative := sim * 0.367
	
	// got should be 0.6335, multiplicative should be ~0.33
	if float64(got) < 0.6 {
		t.Errorf("Confidence value %v suggests a multiplicative formula was used", got)
	}
	if float64(got) < float64(multiplicative) {
		t.Error("Confidence should be higher with additive formula than multiplicative in this case")
	}
}
