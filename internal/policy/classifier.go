package policy

import (
	"regexp"
	"strings"
)

type domainRule struct {
	domain   string
	patterns []*regexp.Regexp
}

// DomainClassifier categorizes queries into predefined domains for policy application.
type DomainClassifier struct {
	rules []domainRule
}

// NewDomainClassifier initializes the classifier with core domain keywords using word boundaries.
func NewDomainClassifier() *DomainClassifier {
	// Fixed priority order for deterministic classification
	domainKeywords := []struct {
		domain string
		words  []string
	}{
		{"coding", []string{"code", "function", "golang", "javascript", "error", "how to", "debug", "library", "implementation"}},
		{"finance", []string{"price", "stock", "market", "finance", "bitcoin", "crypto", "trading", "shares", "nasdaq", "dow jones"}},
		{"weather", []string{"weather", "temperature", "forecast", "rain", "snow", "sunny", "humidity", "wind"}},
		{"medical", []string{"symptom", "pain", "doctor", "health", "medicine", "disease", "treatment", "clinic"}},
		{"legal", []string{"law", "legal", "statute", "court", "lawyer", "attorney", "regulation", "compliance"}},
		{"news", []string{"news", "today", "headlines", "politics", "events", "happened", "current"}},
	}

	rules := make([]domainRule, 0, len(domainKeywords))
	for _, dk := range domainKeywords {
		patterns := make([]*regexp.Regexp, 0, len(dk.words))
		for _, w := range dk.words {
			re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(w) + `\b`)
			patterns = append(patterns, re)
		}
		rules = append(rules, domainRule{domain: dk.domain, patterns: patterns})
	}

	return &DomainClassifier{rules: rules}
}

// Classify determines the domain of a query.
func (c *DomainClassifier) Classify(query string) string {
	q := strings.ToLower(query)

	// Step 1: Keyword fast-path with whole word boundaries
	for _, rule := range c.rules {
		for _, pat := range rule.patterns {
			if pat.MatchString(q) {
				return rule.domain
			}
		}
	}

	// Step 2: Fallback to "general"
	return "general"
}
