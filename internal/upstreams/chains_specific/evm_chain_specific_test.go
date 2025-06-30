package specific_test

import (
	"context"
	"errors"
	"github.com/drpcorg/dsheltie/internal/protocol"
	specific "github.com/drpcorg/dsheltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEvmSubscribeHeadRequest(t *testing.T) {
	req, err := specific.EvmChainSpecific.SubscribeHeadRequest()

	assert.Nil(t, err)
	assert.Equal(t, "1", req.Id())
	assert.Equal(t, "eth_subscribe", req.Method())
	assert.False(t, req.IsStream())
	require.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"eth_subscribe","params":["newHeads"]}`, string(req.Body()))
}

func TestEvmParseSubBLock(t *testing.T) {
	body := []byte(`{
      "number": "0x41fd60b",
      "hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"
    }`)

	block, err := specific.EvmChainSpecific.ParseSubscriptionBlock(body)

	assert.Nil(t, err)
	assert.Equal(t, uint64(69195275), block.BlockData.Height)
	assert.Equal(t, "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18", block.BlockData.Hash)
}

func TestEvmGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x41fd60b",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := specific.EvmChainSpecific.GetLatestBlock(ctx, connector)

	connector.AssertExpectations(t)
	assert.Nil(t, err)
	assert.Equal(t, uint64(69195275), block.BlockData.Height)
	assert.Equal(t, "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18", block.BlockData.Hash)
}

func TestEvmGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := specific.EvmChainSpecific.GetLatestBlock(ctx, connector)

	connector.AssertExpectations(t)
	assert.Nil(t, block)

	var upErr *protocol.ResponseError
	ok := errors.As(err, &upErr)
	assert.True(t, ok)

	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}
