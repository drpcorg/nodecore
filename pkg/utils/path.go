package utils

import (
	"fmt"
	"strings"
)

// BuildRestURL is the inverse of the spec's REST path matcher: given a stored
// template ("VERB#/seg/*") and the wildcard captures the matcher recorded for
// a specific request, it reconstructs the literal HTTP verb and path the
// upstream should be called with.
//
//	BuildRestURL("GET#/v2/accounts/*", []string{"abc"})
//	   -> "GET", "/v2/accounts/abc", nil
//	BuildRestURL("POST#/info", nil)
//	   -> "POST", "/info", nil
//
// Returns an error when the template has more "*" segments than the caller
// provided captures - that's a caller bug, since a successful match always
// returns exactly enough.
func BuildRestURL(template string, pathParams []string) (verb, path string, err error) {
	verb, rest, _ := strings.Cut(template, "#")
	if !strings.Contains(rest, "*") {
		return verb, rest, nil
	}
	segments := strings.Split(rest, "/")
	next := 0
	for i, seg := range segments {
		if seg != "*" {
			continue
		}
		if next >= len(pathParams) {
			return "", "", fmt.Errorf("rest template %q needs more path params than provided (%d)", template, len(pathParams))
		}
		segments[i] = pathParams[next]
		next++
	}
	return verb, strings.Join(segments, "/"), nil
}
