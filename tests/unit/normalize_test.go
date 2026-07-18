package unit

import (
	"os"
	"testing"

	"github.com/Rohit-Bhardwaj10/prism/internal/cache"
)

func TestNormalize(t *testing.T) {
	// Create a temporary synonyms file for testing
	synContent := `
synonyms:
  "nyc": "new york city"
  "ml":  "machine learning"
`
	tmpfile, err := os.CreateTemp("", "synonyms*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(synContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	n := cache.NewNormalizer()
	if err := n.LoadSynonyms(tmpfile.Name()); err != nil {
		t.Fatalf("failed to load synonyms: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Lowercase",
			input:    "HELLO WORLD",
			expected: "hello world",
		},
		{
			name:     "Contraction expansion",
			input:    "what's the weather",
			expected: "what is the weather",
		},
		{
			name:     "Punctuation removal",
			input:    "Hello, world! How's it going?",
			expected: "hello world how is it going",
		},
		{
			name:     "Synonym substitution",
			input:    "weather in nyc",
			expected: "weather in new york city",
		},
		{
			name:     "Whitespace collapsing",
			input:    "  hello    world  ",
			expected: "hello world",
		},
		{
			name:     "Word order preserved (nyc weather)",
			input:    "nyc weather",
			expected: "new york city weather",
		},
		{
			name:     "Word order preserved (weather nyc)",
			input:    "weather nyc",
			expected: "weather new york city",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := n.Normalize(tt.input)
			if got != tt.expected {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}

	// Double check that "weather nyc" != "nyc weather" after normalization
	if n.Normalize("weather nyc") == n.Normalize("nyc weather") {
		t.Error("expected 'weather nyc' and 'nyc weather' to remain distinct (not sorted)")
	}
}
