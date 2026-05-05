package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestNewUpstreamRestRequestEncodesVerbAndPath(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("GET", "/v2/status", nil)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/status", req.Method())
	assert.Equal(t, protocol.Rest, req.RequestType())
	assert.NotEmpty(t, req.Id(), "id must be initialised so concurrent observers don't collide")
	assert.NotNil(t, req.RequestObserver(), "observer must be non-nil so ObserverConnector doesn't panic")
}

func TestNewUpstreamRestRequestForwardsBody(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	req := protocol.NewUpstreamRestRequest("POST", "/v2/transactions", body)

	got, err := req.Body()
	assert.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, "POST"+protocol.MethodSeparator+"/v2/transactions", req.Method())
}

func TestNewUpstreamRestRequestEmptyBodyIsAllowed(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("GET", "/v2/status", nil)

	got, err := req.Body()
	assert.NoError(t, err)
	assert.Empty(t, got, "GET requests forward an empty body")
}

func TestNewUpstreamRestRequestVerbDefaultsToGet(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("", "/v2/status", nil)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/status", req.Method(),
		"empty verb must default to GET so callers that forget to set a method don't blow up downstream")
}

func TestNewUpstreamRestRequestVerbIsUppercased(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("post", "/v2/transactions", []byte(`{}`))

	assert.Equal(t, "POST"+protocol.MethodSeparator+"/v2/transactions", req.Method())
}

func TestNewUpstreamRestRequestPrefixesLeadingSlash(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("GET", "v2/status", nil)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/status", req.Method(),
		"path without leading slash must be normalised so HttpConnector can build a valid URL")
}

func TestNewUpstreamRestRequestUniqueIds(t *testing.T) {
	a := protocol.NewUpstreamRestRequest("GET", "/v2/status", nil)
	b := protocol.NewUpstreamRestRequest("GET", "/v2/status", nil)

	assert.NotEqual(t, a.Id(), b.Id(),
		"each external REST request must get its own id so observer state doesn't collide")
}

func TestNewUpstreamRestRequestNotStreamingNotSubscribe(t *testing.T) {
	req := protocol.NewUpstreamRestRequest("GET", "/v2/status", nil)

	assert.False(t, req.IsStream())
	assert.False(t, req.IsSubscribe())
}

func TestNewInternalUpstreamRestRequestEncodesVerbAndPath(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequest("GET", "/v2/blocks/1/hash", chains.ALGORAND)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/blocks/1/hash", req.Method())
	assert.Equal(t, protocol.Rest, req.RequestType())
}

func TestNewInternalUpstreamRestRequestWithQueryAppendsParams(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequestWithQuery(
		"GET",
		"/v2/blocks/1",
		map[string]string{"header-only": "true"},
		chains.ALGORAND,
	)

	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/blocks/1?header-only=true", req.Method())
}

func TestNewInternalUpstreamRestRequestWithQueryAppendsParamsToExistingQuery(t *testing.T) {
	req := protocol.NewInternalUpstreamRestRequestWithQuery(
		"GET",
		"/v2/blocks/1?format=json",
		map[string]string{"header-only": "true"},
		chains.ALGORAND,
	)

	// existing query string is preserved; new params join with `&`
	assert.Contains(t, req.Method(), "?format=json&header-only=true")
}
