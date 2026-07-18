package unit

import (
	"testing"
	"github.com/Rohit-Bhardwaj10/prism/internal/policy"
)

func TestTemporalClass(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"Today keyword", "weather today in london", policy.TemporalPresent},
		{"Now keyword", "what is the price now", policy.TemporalPresent},
		{"Tomorrow keyword", "forecast for tomorrow", policy.TemporalFuture},
		{"Next week", "news for next week", policy.TemporalFuture},
		{"Yesterday", "what happened yesterday", policy.TemporalPast},
		{"Last month", "sales last month", policy.TemporalPast},
		{"No temporal", "how to write a loop in go", policy.TemporalNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.TemporalClass(tt.query)
			if got != tt.want {
				t.Errorf("TemporalClass(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
