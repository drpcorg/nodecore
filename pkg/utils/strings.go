package utils

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

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

// ToValidUTF8 replaces every run of invalid UTF-8 bytes in s with U+FFFD.
// Use it on client-controlled strings before handing them to code that requires
// valid UTF-8 - Prometheus label values (client_golang panics on invalid ones,
// which kills the process) and proto3 string fields (proto.Marshal fails).
//
// Valid input is returned as is, without allocating.
func ToValidUTF8(s string) string {
	return strings.ToValidUTF8(s, string(utf8.RuneError))
}
