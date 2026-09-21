package ws

import (
	"encoding/json"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This case lives in the internal test package so it can assert the sentinel with
// errors.Is, which RequestFrame's %w wrapping makes possible. The fall-through
// cases stay in ws_protocol_test.go: they assert no error, so they need no access
// to the sentinel. No spec loading is needed here - isSub is passed explicitly, so
// getSubscription reaches the eth_subscribe branch without consulting the specs.

// The eth_subscribe subscription type becomes the "subscription" label of
// nodecore_request_json_ws_connections. WithLabelValues panics on an invalid label
// value, and nothing recovers, so it would crash nodecore - and a raw invalid byte
// does reach here: it survives both sonic's decode and Body()'s marshal. Reject the
// frame instead of labelling with it.
func TestJsonRpcWsProtocolRequestFrameRejectsNonUtf8SubType(t *testing.T) {
	wsProtocol := NewJsonRpcWsProtocol("upstream-1", "eth", chains.ETHEREUM)

	request := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{
		Id:      json.RawMessage(`1`),
		Jsonrpc: "2.0",
		Method:  "eth_subscribe",
		Params:  json.RawMessage("[\"new\xffHeads\"]"),
	}, true, "eth")

	frame, err := wsProtocol.RequestFrame(request)

	require.ErrorIs(t, err, errNonUtf8SubType)
	assert.Nil(t, frame)
}
