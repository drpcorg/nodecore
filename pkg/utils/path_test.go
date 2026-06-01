package utils_test

import (
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestPathMatcher_LiteralMatch(t *testing.T) {
	m := utils.NewPathMatcher([]string{"GET#/v2/status"})
	template, params, ok := m.Match("GET#/v2/status")
	assert.True(t, ok)
	assert.Equal(t, "GET#/v2/status", template)
	assert.Empty(t, params)
}

func TestPathMatcher_WildcardCapture(t *testing.T) {
	m := utils.NewPathMatcher([]string{"GET#/v2/accounts/*"})
	template, params, ok := m.Match("GET#/v2/accounts/X1Y2Z3")
	assert.True(t, ok)
	assert.Equal(t, "GET#/v2/accounts/*", template)
	assert.Equal(t, []string{"X1Y2Z3"}, params)
}

func TestPathMatcher_MultipleWildcards(t *testing.T) {
	m := utils.NewPathMatcher([]string{"GET#/v2/accounts/*/txs/*"})
	template, params, ok := m.Match("GET#/v2/accounts/abc/txs/42")
	assert.True(t, ok)
	assert.Equal(t, "GET#/v2/accounts/*/txs/*", template)
	assert.Equal(t, []string{"abc", "42"}, params)
}

func TestPathMatcher_LiteralBeatsWildcard(t *testing.T) {
	m := utils.NewPathMatcher([]string{
		"GET#/blocks/latest",
		"GET#/blocks/*",
	})

	template, params, ok := m.Match("GET#/blocks/latest")
	assert.True(t, ok)
	assert.Equal(t, "GET#/blocks/latest", template, "literal child must win over wildcard at the same level")
	assert.Empty(t, params)

	template, params, ok = m.Match("GET#/blocks/42")
	assert.True(t, ok)
	assert.Equal(t, "GET#/blocks/*", template)
	assert.Equal(t, []string{"42"}, params)
}

func TestPathMatcher_DifferentVerbs(t *testing.T) {
	m := utils.NewPathMatcher([]string{
		"GET#/v2/transactions",
		"POST#/v2/transactions",
	})
	for verb, want := range map[string]string{
		"GET":  "GET#/v2/transactions",
		"POST": "POST#/v2/transactions",
	} {
		template, params, ok := m.Match(verb + "#/v2/transactions")
		assert.True(t, ok)
		assert.Equal(t, want, template)
		assert.Empty(t, params)
	}
}

func TestPathMatcher_NoMatchReturnsFalse(t *testing.T) {
	m := utils.NewPathMatcher([]string{"GET#/v2/status"})

	_, _, ok := m.Match("GET#/v2/unknown")
	assert.False(t, ok, "unregistered path must not match")

	_, _, ok = m.Match("POST#/v2/status")
	assert.False(t, ok, "wrong verb must not match")

	_, _, ok = m.Match("GET#/v2/status/extra")
	assert.False(t, ok, "extra trailing segment must not match a shorter template")
}

func TestPathMatcher_PartialMatchOnNonTerminatingNodeIsMiss(t *testing.T) {
	// Only the full /v2/accounts/* path is registered. /v2/accounts on its own
	// must not match - the intermediate node is not terminating.
	m := utils.NewPathMatcher([]string{"GET#/v2/accounts/*"})
	_, _, ok := m.Match("GET#/v2/accounts")
	assert.False(t, ok)
}

func TestPathMatcher_TolerantOfSlashesAndEmptySegments(t *testing.T) {
	m := utils.NewPathMatcher([]string{"GET#/v2/status"})

	template, _, ok := m.Match("GET#//v2//status")
	assert.True(t, ok, "duplicate slashes must be normalised away just like the template loader does it")
	assert.Equal(t, "GET#/v2/status", template)
}

func TestPathMatcher_DuplicateRegistrationIsHarmless(t *testing.T) {
	m := utils.NewPathMatcher([]string{
		"GET#/v2/status",
		"GET#/v2/status",
	})
	template, _, ok := m.Match("GET#/v2/status")
	assert.True(t, ok)
	assert.Equal(t, "GET#/v2/status", template)
}
