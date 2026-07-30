package http_server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two method-rejection tests live in the internal test package so they can
// assert the sentinel with errors.Is instead of matching its message text - the
// same reason rest_parser_test.go can. The accept-side cases stay in
// handlers_test.go: they assert no error, so they need no access to the sentinel.

// A raw invalid UTF-8 byte in "method" survives sonic's decode untouched, so it
// would flow into a metric label and make WithLabelValues panic - and nothing
// recovers, so it would crash nodecore. Reject it at parse time.
func TestJsonRpcHandlerRejectsNonUtf8Method(t *testing.T) {
	body := "{\"id\":1,\"jsonrpc\":\"2.0\",\"method\":\"eth_\xffblockNumber\",\"params\":[]}"

	handler, err := NewJsonRpcHandler(
		&Request{Chain: "ethereum"},
		strings.NewReader(body),
		false,
	)

	require.ErrorIs(t, err, errNonUtf8Method)
	assert.Nil(t, handler)
}

// One bad entry rejects the whole batch: the constructor fails before any work is
// scheduled, which is how every other parse failure already behaves.
func TestJsonRpcHandlerRejectsNonUtf8MethodInBatch(t *testing.T) {
	body := "[{\"id\":1,\"jsonrpc\":\"2.0\",\"method\":\"eth_blockNumber\",\"params\":[]}," +
		"{\"id\":2,\"jsonrpc\":\"2.0\",\"method\":\"eth_\xffcall\",\"params\":[]}]"

	handler, err := NewJsonRpcHandler(
		&Request{Chain: "ethereum"},
		strings.NewReader(body),
		false,
	)

	require.ErrorIs(t, err, errNonUtf8Method)
	assert.Nil(t, handler)
}
