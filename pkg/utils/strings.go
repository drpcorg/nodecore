package utils

import (
	"regexp"
	"strings"
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
