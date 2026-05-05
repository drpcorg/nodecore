package server_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	servernodecore "github.com/drpcorg/nodecore/internal/server"
	"github.com/stretchr/testify/assert"
)

// TestRestHandlerAcceptsEmptyBody is the regression test for the "couldn't
// parse a request" bug: every REST GET arrived with an empty body, but the
// old NewRestHandler ran sonic.Valid([]byte{}) which is false, so it always
// short-circuited with parse error. Algod and other REST upstreams could
// never be reached.
func TestRestHandlerAcceptsEmptyBody(t *testing.T) {
	handler, err := servernodecore.NewRestHandler(
		&servernodecore.Request{Chain: "algorand-testnet"},
		"GET",
		"/v2/status",
		bytes.NewReader(nil),
	)

	assert.NoError(t, err, "empty body must not be rejected for GET-style requests")
	assert.NotNil(t, handler)
	assert.True(t, handler.IsSingle())
	assert.Equal(t, 1, handler.RequestCount())
	assert.Equal(t, protocol.Rest, handler.GetRequestType())
}

func TestRestHandlerAcceptsValidJsonBody(t *testing.T) {
	handler, err := servernodecore.NewRestHandler(
		&servernodecore.Request{Chain: "algorand-testnet"},
		"POST",
		"/v2/transactions",
		strings.NewReader(`{"raw":"AAA"}`),
	)

	assert.NoError(t, err)
	assert.NotNil(t, handler)
}

func TestRestHandlerRejectsMalformedJsonBody(t *testing.T) {
	_, err := servernodecore.NewRestHandler(
		&servernodecore.Request{Chain: "algorand-testnet"},
		"POST",
		"/v2/transactions",
		strings.NewReader(`{not json`),
	)

	assert.Error(t, err, "non-empty bodies must still be validated as JSON")
}

func TestRestHandlerRequestDecodeForwardsVerbAndPath(t *testing.T) {
	handler, err := servernodecore.NewRestHandler(
		&servernodecore.Request{Chain: "algorand-testnet"},
		"GET",
		"/v2/status",
		bytes.NewReader(nil),
	)
	assert.NoError(t, err)

	request, err := handler.RequestDecode(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "algorand-testnet", request.Chain)
	assert.Len(t, request.UpstreamRequests, 1)

	up := request.UpstreamRequests[0]
	assert.Equal(t, "GET"+protocol.MethodSeparator+"/v2/status", up.Method(),
		"upstream method must encode the original verb + path so HttpConnector forwards correctly")
	assert.Equal(t, protocol.Rest, up.RequestType())
	body, err := up.Body()
	assert.NoError(t, err)
	assert.Empty(t, body)
}

func TestRestHandlerRequestDecodeForwardsBody(t *testing.T) {
	payload := []byte(`{"raw":"AAA"}`)
	handler, err := servernodecore.NewRestHandler(
		&servernodecore.Request{Chain: "algorand-testnet"},
		"POST",
		"/v2/transactions",
		bytes.NewReader(payload),
	)
	assert.NoError(t, err)

	request, err := handler.RequestDecode(context.Background())
	assert.NoError(t, err)
	assert.Len(t, request.UpstreamRequests, 1)

	up := request.UpstreamRequests[0]
	assert.Equal(t, "POST"+protocol.MethodSeparator+"/v2/transactions", up.Method())
	body, err := up.Body()
	assert.NoError(t, err)
	assert.Equal(t, payload, body)
}

func TestRestHandlerForwardsPathWithQueryString(t *testing.T) {
	handler, err := servernodecore.NewRestHandler(
		&servernodecore.Request{Chain: "algorand-testnet"},
		"GET",
		"/v2/blocks/1?header-only=true",
		bytes.NewReader(nil),
	)
	assert.NoError(t, err)

	request, err := handler.RequestDecode(context.Background())
	assert.NoError(t, err)
	assert.Equal(t,
		"GET"+protocol.MethodSeparator+"/v2/blocks/1?header-only=true",
		request.UpstreamRequests[0].Method(),
		"query string must reach the upstream so e.g. ?header-only=true is preserved",
	)
}
