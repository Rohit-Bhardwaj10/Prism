package cache

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	punctuationRegex = regexp.MustCompile(`[^\w\s]`)
	whitespaceRegex  = regexp.MustCompile(`\s+`)
)

type synonymRule struct {
	key string
	val string
	re  *regexp.Regexp
}

// Normalizer handles L0 normalization of query strings.
type Normalizer struct {
	mu       sync.RWMutex
	synonyms map[string]string
	rules    []synonymRule
}

// SynonymConfig matches the structure of configs/synonyms.yaml
type SynonymConfig struct {
	Synonyms map[string]string `yaml:"synonyms"`
}

// NewNormalizer creates a new Normalizer.
func NewNormalizer() *Normalizer {
	return &Normalizer{
		synonyms: make(map[string]string),
	}
}

// LoadSynonyms loads synonym mappings from a YAML file.
func (n *Normalizer) LoadSynonyms(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var config SynonymConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	n.synonyms = config.Synonyms

	// Sort keys by descending length for greedy longest-match replacement,
	// and alphabetically for deterministic tie-breaking.
	keys := make([]string, 0, len(config.Synonyms))
	for k := range config.Synonyms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})

	n.rules = make([]synonymRule, 0, len(keys))
	for _, k := range keys {
		v := config.Synonyms[k]
		// Match whole words/phrases using word boundaries \b
		pattern := `\b` + regexp.QuoteMeta(k) + `\b`
		re, err := regexp.Compile(pattern)
		if err == nil {
			n.rules = append(n.rules, synonymRule{key: k, val: v, re: re})
		}
	}

	return nil
}

// Normalize applies the L0 normalization pipeline.
func (n *Normalizer) Normalize(query string) string {
	// Step 1: Lowercase
	res := strings.ToLower(query)

	// Step 2: Expand contractions
	res = expandContractions(res)

	// Step 3: Remove punctuation (strip non-alphanumeric)
	res = punctuationRegex.ReplaceAllString(res, "")

	// Step 4: Apply synonyms
	res = n.applySynonyms(res)

	// Step 5: Collapse whitespace
	res = whitespaceRegex.ReplaceAllLiteralString(res, " ")
	res = strings.TrimSpace(res)

	return res
}

func expandContractions(s string) string {
	// A basic set of common English contractions.
	// In a real system, this might be more exhaustive.
	replacements := map[string]string{
		"it's":    "it is",
		"that's":  "that is",
		"what's":  "what is",
		"where's": "where is",
		"who's":   "who is",
		"how's":   "how is",
		"i'm":     "i am",
		"you're":  "you are",
		"he's":    "he is",
		"she's":   "she is",
		"we're":   "we are",
		"they're": "they are",
		"can't":   "cannot",
		"won't":   "will not",
		"don't":   "do not",
		"doesn't": "does not",
		"didn't":  "did not",
		"itll":    "it will",
		"thatll":  "that will",
		"youve":   "you have",
		"ive":     "i have",
	}

	for k, v := range replacements {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

func (n *Normalizer) applySynonyms(s string) string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for _, rule := range n.rules {
		s = rule.re.ReplaceAllString(s, rule.val)
	}
	return s
}
