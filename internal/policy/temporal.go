package policy

import (
	"regexp"
	"strings"
)

const (
	TemporalPresent = "present"
	TemporalFuture  = "future"
	TemporalPast    = "past"
	TemporalNone    = ""
)

var (
	futureRegex  = regexp.MustCompile(`(?i)\b(tomorrow|next week|next month|upcoming)\b`)
	pastRegex    = regexp.MustCompile(`(?i)\b(yesterday|last week|last month|previously)\b`)
	presentRegex = regexp.MustCompile(`(?i)\b(today|tonight|now|currently|right now)\b`)
)

// TemporalClass extracts the temporal context from a normalized query string.
func TemporalClass(query string) string {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return TemporalNone
	}

	// Future checks
	if futureRegex.MatchString(q) {
		return TemporalFuture
	}

	// Past checks
	if pastRegex.MatchString(q) {
		return TemporalPast
	}

	// Present checks
	if presentRegex.MatchString(q) {
		return TemporalPresent
	}

	return TemporalNone
}
