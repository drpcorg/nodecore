package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestNewUpstreamRestRequestStoresTemplateVerbatim(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/status", nil, nil, "")

	assert.Equal(t, "GET#/v2/status", req.Method())
	assert.Equal(t, protocol.Rest, req.RequestType())
	assert.NotEmpty(t, req.Id(), "id must be initialised so concurrent observers don't collide")
	assert.NotNil(t, req.RequestObserver(), "observer must be non-nil so ObserverConnector doesn't panic")
}

func TestNewUpstreamRestRequestForwardsBody(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	req := protocol.NewUpstreamRestRequest("test-id", "POST#/v2/transactions", nil, body, "")

	got, err := req.Body()
	assert.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestNewUpstreamRestRequestEmptyBodyIsAllowed(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/status", nil, nil, "")

	got, err := req.Body()
	assert.NoError(t, err)
	assert.Empty(t, got, "GET requests forward an empty body")
}

func TestNewUpstreamRestRequestCarriesTemplateWithWildcard(t *testing.T) {
	rp := &protocol.RequestParams{PathParams: []string{"X1Y2"}}
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/accounts/*", rp, nil, "")

	assert.Equal(t, "GET#/v2/accounts/*", req.Method(),
		"the template is the canonical method - it's what spec lookup, stats, and caching key on")
	assert.Equal(t, []string{"X1Y2"}, req.RequestParams().PathParams,
		"path captures live on RequestParams so the connector can rebuild the literal URL at send time")
}

func TestNewUpstreamRestRequestNotStreamingNotSubscribe(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("test-id", "GET#/v2/status", nil, nil, "")

	assert.False(t, req.IsStream())
	assert.False(t, req.IsSubscribe())
}

func TestNewInternalUpstreamRestRequestEncodesVerbAndPath(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequest("GET", "/v2/blocks/1/hash", chains.ALGORAND)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/blocks/1/hash", req.Method())
	assert.Equal(t, protocol.Rest, req.RequestType())
	assert.Nil(t, req.RequestParams(), "internal request without a query has no RequestParams to carry")
}

func TestNewInternalUpstreamRestRequestSplitsQueryOffMethod(t *testing.T) {
	// algorand_chain_specific.go builds paths like "/v2/blocks/42?header-only=true";
	// the constructor must strip that into RequestParams so Method() is a clean
	// template the connector can pass to BuildRestURL.
	req := protocol.NewInternalUpstreamRestRequest("GET", "/v2/blocks/42?header-only=true", chains.ALGORAND)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/blocks/42", req.Method(),
		"the query must be split off so Method() stays a clean template")
	assert.Equal(t, []string{"true"}, req.RequestParams().QueryParams["header-only"])
}

func TestNewInternalUpstreamRestRequestVerbDefaultsToGet(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequest("", "/v2/status", chains.ALGORAND)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/status", req.Method(),
		"empty verb must default to GET so callers that forget to set a method don't blow up downstream")
}

func TestNewInternalUpstreamRestRequestVerbIsUppercased(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequest("post", "/v2/transactions", chains.ALGORAND)

	assert.Equal(t, "POST"+protocol.MethodSeparator+"/v2/transactions", req.Method())
}

func TestNewInternalUpstreamRestRequestPrefixesLeadingSlash(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequest("GET", "v2/status", chains.ALGORAND)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/status", req.Method(),
		"path without leading slash must be normalised so BuildRestURL produces a valid URL")
}

func TestNewInternalUpstreamRestRequestWithQueryStashesParams(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequestWithQuery(
		"GET",
		"/v2/blocks/1",
		map[string]string{"header-only": "true"},
		chains.ALGORAND,
	)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/blocks/1", req.Method(),
		"query params must NOT be baked into Method() any more - they live on RequestParams")
	assert.Equal(t, []string{"true"}, req.RequestParams().QueryParams["header-only"])
}

func TestNewInternalUpstreamRestRequestWithQueryMergesWithExistingQuery(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequestWithQuery(
		"GET",
		"/v2/blocks/1?format=json",
		map[string]string{"header-only": "true"},
		chains.ALGORAND,
	)

	rp := req.RequestParams()
	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/blocks/1", req.Method())
	assert.Equal(t, []string{"json"}, rp.QueryParams["format"])
	assert.Equal(t, []string{"true"}, rp.QueryParams["header-only"])
}
