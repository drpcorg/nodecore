package specific_helpers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const headerJSON = `{
	"parentHash": "0x1b3a...aa",
	"number": "0x1a2b3c",
	"stateRoot": "0xcc",
	"extrinsicsRoot": "0xdd",
	"digest": {
		"logs": ["0x01"]
	}
}`

func TestParsePolkadotHeader(t *testing.T) {
	header, err := specific_helpers.ParsePolkadotHeader([]byte(headerJSON))
	require.NoError(t, err)
	assert.Equal(t, "0x1a2b3c", header.Number)
	assert.Equal(t, "0x1b3a...aa", header.ParentHash)
}

func TestParsePolkadotHeaderInvalidJson(t *testing.T) {
	header, err := specific_helpers.ParsePolkadotHeader([]byte(`not json`))
	assert.Nil(t, header)
	assert.ErrorContains(t, err, "polkadot header payload unparseable")
}

// A header with no number is unusable - it carries no height, and without a
// height there is no chain_getBlockHash argument either.
func TestParsePolkadotHeaderMissingNumber(t *testing.T) {
	header, err := specific_helpers.ParsePolkadotHeader([]byte(`{"parentHash":"0xaa"}`))
	assert.Nil(t, header)
	assert.ErrorContains(t, err, "polkadot header has no number")
}

func TestParsePolkadotHeight(t *testing.T) {
	tests := []struct {
		name   string
		number string
		want   uint64
	}{
		{"prefixed", "0x1a2b3c", 1715004},
		{"unprefixed", "1a2b3c", 1715004},
		{"zero", "0x0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			height, err := specific_helpers.ParsePolkadotHeight(tt.number)
			require.NoError(t, err)
			assert.Equal(t, tt.want, height)
		})
	}
}

func TestParsePolkadotHeightInvalid(t *testing.T) {
	for _, number := range []string{"", "0x", "0xzz", "not a number"} {
		_, err := specific_helpers.ParsePolkadotHeight(number)
		assert.Error(t, err, "expected %q to be rejected", number)
	}
}

func matchPolkadotMethod(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method
	}
}

func TestFetchPolkadotHeader(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchPolkadotMethod("chain_getHeader"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(headerJSON), protocol.JsonRpc))

	header, err := specific_helpers.FetchPolkadotHeader(context.Background(), connector, chains.GetChain("polkadot").Chain)
	require.NoError(t, err)
	assert.Equal(t, "0x1a2b3c", header.Number)
	connector.AssertExpectations(t)
}

func TestFetchPolkadotHeaderUpstreamError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	header, err := specific_helpers.FetchPolkadotHeader(context.Background(), connector, chains.GetChain("polkadot").Chain)
	assert.Nil(t, header)
	assert.Error(t, err)
}

// chain_getBlockHash takes the header's number verbatim (the hex string), which
// sidesteps any decimal-vs-hex ambiguity in the node's param handling.
func TestFetchPolkadotBlockHashPassesNumberVerbatim(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		if req.Method() != "chain_getBlockHash" {
			return false
		}
		body, err := req.Body()
		return err == nil && strings.Contains(string(body), `["0x1a2b3c"]`)
	})).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xdeadbeef"`), protocol.JsonRpc))

	hash, err := specific_helpers.FetchPolkadotBlockHash(
		context.Background(), connector, chains.GetChain("polkadot").Chain, "0x1a2b3c",
	)
	require.NoError(t, err)
	assert.Equal(t, "0xdeadbeef", hash)
	connector.AssertExpectations(t)
}

func TestFetchPolkadotBlockHashEmptyResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`""`), protocol.JsonRpc))

	hash, err := specific_helpers.FetchPolkadotBlockHash(
		context.Background(), connector, chains.GetChain("polkadot").Chain, "0x1a2b3c",
	)
	assert.Empty(t, hash)
	assert.ErrorContains(t, err, "empty block hash")
}
