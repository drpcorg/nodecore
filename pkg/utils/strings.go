package utils

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// SanitizeMetricLabel makes s safe to use as a Prometheus label value.
//
// client_golang panics ("label value ... is not valid UTF-8") when a label
// value contains invalid UTF-8, and that panic happens inside detached
// goroutines with no recover, so it crashes the whole process. Any label value
// derived from untrusted input - most notably a REST request method, which is
// "<VERB>#<path>" built from the raw request URI - must be passed through this
// first. Valid input is returned unchanged; invalid byte sequences are replaced
// with the Unicode replacement character.
func SanitizeMetricLabel(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

func WildcardToRegex(pattern string) string {
	components := strings.Split(pattern, "*")
	if len(components) == 1 {
		// if len is 1, there are no *'s, return exact match pattern
		return "^" + pattern + "$"
	}
	var result string
	for i, literal := range components {

		// Replace * with .*
		if i > 0 {
			result += ".*"
		}

		// Quote any regular expression meta characters in the
		// literal text.
		result += regexp.QuoteMeta(literal)
	}
	return "^" + result + "$"
}

func MatchWildcards(pattern string, value string) bool {
	result, _ := regexp.MatchString(WildcardToRegex(pattern), value)
	return result
}
