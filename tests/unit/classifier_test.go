package unit

import (
	"testing"
	"github.com/Rohit-Bhardwaj10/prism/internal/policy"
)

func TestDomainClassifier(t *testing.T) {
	c := policy.NewDomainClassifier()

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"Weather query", "what is the temperature", "weather"},
		{"Finance query", "bitcoin price performance", "finance"},
		{"Medical query", "doctor for symptoms", "medical"},
		{"Unknown query", "some random query", "general"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.Classify(tt.query)
			if got != tt.want {
				t.Errorf("Classify(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
