package http_server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file run against the dedicated `test_specs/rest.json`
// fixture (spec name: "rest-test") loaded in TestMain. The fixture
// intentionally exercises wildcards, multi-verb collisions, and
// literal-beats-wildcard - things the embedded production specs don't
// currently cover.

func TestParseRestRequest_MatchedLiteralTemplate(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/exchange", nil)

	template, rp, err := parseRestRequest(req, "exchange", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "POST#/exchange", template, "matched literal template becomes the canonical method")
	require.NotNil(t, rp)
	assert.Empty(t, rp.PathParams)
}

func TestParseRestRequest_WildcardCapturesPathParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v2/accounts/X1Y2Z3", nil)

	template, rp, err := parseRestRequest(req, "v2/accounts/X1Y2Z3", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "GET#/v2/accounts/*", template,
		"the canonical method is the template - the literal segment lives in PathParams")
	require.NotNil(t, rp)
	assert.Equal(t, []string{"X1Y2Z3"}, rp.PathParams,
		"PathParams must reach RequestParams so the connector can rebuild the URL")
}

func TestParseRestRequest_MultipleWildcardsCaptureInOrder(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v2/blocks/42/tx/abc", nil)

	template, rp, err := parseRestRequest(req, "v2/blocks/42/tx/abc", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "GET#/v2/blocks/*/tx/*", template)
	require.NotNil(t, rp)
	assert.Equal(t, []string{"42", "abc"}, rp.PathParams)
}

// Literal child wins over a wildcard sibling at the same trie level.
// Without this, /v2/accounts/latest would be ambiguous with the wildcard
// route - the matcher's no-backtrack rule resolves it deterministically.
func TestParseRestRequest_LiteralBeatsWildcardAtSameLevel(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v2/accounts/latest", nil)

	template, rp, err := parseRestRequest(req, "v2/accounts/latest", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "GET#/v2/accounts/latest", template,
		"literal child must win over the wildcard sibling at the same level")
	require.NotNil(t, rp)
	assert.Empty(t, rp.PathParams)
}

// Different verbs on similar paths must route independently - the verb is
// the first trie segment.
func TestParseRestRequest_VerbPartitionsTrie(t *testing.T) {
	delReq := httptest.NewRequest(http.MethodDelete, "/v2/sessions/abc123", nil)

	template, _, err := parseRestRequest(delReq, "v2/sessions/abc123", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "DELETE#/v2/sessions/*", template,
		"DELETE template must match - GET on the same path would not exist")
}

func TestParseRestRequest_WrongVerbFallsBackToLiteral(t *testing.T) {
	// /v2/accounts/* is registered for GET only. A POST to the same path
	// has no template and falls through to the literal template policy.
	req := httptest.NewRequest(http.MethodPost, "/v2/accounts/X1Y2Z3", nil)

	template, _, err := parseRestRequest(req, "v2/accounts/X1Y2Z3", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "POST#/v2/accounts/X1Y2Z3", template,
		"wrong verb on a registered path must still fall through to a literal")
}

// Spec exists and has REST routes but the path matches nothing - parser
// must still produce a usable canonical template so stats/caching key off
// something deterministic.
func TestParseRestRequest_UnmatchedFallsBackToLiteral(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/totally/unknown/path", nil)

	template, rp, err := parseRestRequest(req, "totally/unknown/path", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "GET#/totally/unknown/path", template,
		"miss on a spec with REST routes still falls through to a literal template")
	require.NotNil(t, rp)
}

// Specs that don't model REST at all (unknown spec name) should also
// produce a usable template - the connector path operates on the literal
// verb+path verbatim.
func TestParseRestRequest_UnknownSpecFallsBackToLiteral(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v2/anything", nil)

	template, rp, err := parseRestRequest(req, "v2/anything", "no-such-spec")
	require.NoError(t, err)
	assert.Equal(t, "GET#/v2/anything", template)
	require.NotNil(t, rp)
}

func TestParseRestRequest_StripsReservedQueryParams(t *testing.T) {
	// quorum / quorum_required are nodecore control params and must not be
	// forwarded to the upstream. Everything else passes through, multi-valued
	// shape preserved.
	req := httptest.NewRequest(http.MethodPost, "/exchange?quorum=3&quorum_required=2&token=A&token=B", nil)

	_, rp, err := parseRestRequest(req, "exchange", "rest-test")
	require.NoError(t, err)
	require.NotNil(t, rp)
	assert.NotContains(t, rp.QueryParams, "quorum",
		"reserved control params must be stripped before forwarding")
	assert.NotContains(t, rp.QueryParams, "quorum_required")
	assert.Equal(t, []string{"A", "B"}, rp.QueryParams["token"],
		"repeated values must survive the round-trip")
}

func TestParseRestRequest_CopiesHeadersIncludingRepeats(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/exchange", nil)
	req.Header.Set("X-Custom", "hello")
	req.Header.Add("X-Multi", "one")
	req.Header.Add("X-Multi", "two")

	_, rp, err := parseRestRequest(req, "exchange", "rest-test")
	require.NoError(t, err)
	require.NotNil(t, rp)
	assert.Equal(t, []string{"hello"}, rp.Headers["X-Custom"])
	assert.Equal(t, []string{"one", "two"}, rp.Headers["X-Multi"],
		"repeated headers must survive the round-trip")
}

// fullPath is built as req.Method + "#" + "/" + restPath. Regression-guard
// the join so we never end up with an empty rest segment in the canonical
// template.
func TestParseRestRequest_RestPathWithoutLeadingSlash(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/exchange", nil)

	template, _, err := parseRestRequest(req, "exchange", "rest-test")
	require.NoError(t, err)
	assert.Equal(t, "POST#/exchange", template, "no double slash in the canonical template")
}

// nodecore's credential headers are consumed by the ingress and must never
// enter the request holder; everything else is forwarded
func TestParseRestRequestStripsReservedCredentialHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v2/status", nil)
	req.Header.Set("X-Nodecore-Key", "secret")
	req.Header.Set("X-Nodecore-Token", "token")
	req.Header.Set("Authorization", "Bearer jwt")
	req.Header.Set("X-Custom", "keep")

	_, requestParams, err := parseRestRequest(req, "v2/status", "aptos")
	require.NoError(t, err)

	headers := http.Header(requestParams.Headers)
	assert.Equal(t, "keep", headers.Get("X-Custom"))
	assert.Empty(t, headers.Get("X-Nodecore-Key"))
	assert.Empty(t, headers.Get("X-Nodecore-Token"))
	assert.Empty(t, headers.Get("Authorization"))
}
