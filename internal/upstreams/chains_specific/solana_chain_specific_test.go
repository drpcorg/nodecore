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

func TestSolanaSubscribeHeadRequest(t *testing.T) {
	req, err := specific.SolanaChainSpecific.SubscribeHeadRequest()

	assert.Nil(t, err)
	assert.Equal(t, "1", req.Id())
	assert.Equal(t, "blockSubscribe", req.Method())
	assert.False(t, req.IsStream())
	require.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"blockSubscribe","params":["all",{"showRewards":false,"transactionDetails":"none"}]}`, string(req.Body()))
}

func TestSolanaParseSubBLock(t *testing.T) {
	body := []byte(`{
      "context": {
        "slot": 327557189
      },
      "value": {
        "slot": 327557189,
        "block": {
          "previousBlockhash": "5SFHqjdrjZdydRF8Cey9Zgp4CCX9ELifUSvjJV7kacnn",
          "blockhash": "2XB8V5eP7HaNeRd2u98YLYS7QqzX61MNskmzeyXy4oiG",
          "parentSlot": 327557188,
          "blockTime": 1742296365,
          "blockHeight": 305813576
        },
        "err": null
      }
    }`)

	block, err := specific.SolanaChainSpecific.ParseSubscriptionBlock(body)

	assert.Nil(t, err)
	assert.Equal(t, uint64(305813576), block.BlockData.Height)
	assert.Equal(t, uint64(327557189), block.BlockData.Slot)
	assert.Equal(t, "2XB8V5eP7HaNeRd2u98YLYS7QqzX61MNskmzeyXy4oiG", block.BlockData.Hash)
}

func TestSolanaGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	slotBody := []byte(`{
		"id": 1,
		"jsonrpc": "2.0",
		"result": 327557752
	}`)
	slotResponse := protocol.NewHttpUpstreamResponse("1", slotBody, 200, protocol.JsonRpc)
	maxBlocksBody := []byte(`{
		"id": 1,
		"jsonrpc": "2.0",
		"result": [327557750, 327557751, 327557752]
	}`)
	maxBlocksResponse := protocol.NewHttpUpstreamResponse("1", maxBlocksBody, 200, protocol.JsonRpc)
	blockBody := []byte(`{
		"id": 1,
		"jsonrpc": "2.0",
		"result": {
			"blockHeight": 305814139,
			"blockhash": "7QbMXETjcbRHTLxqAEH62nGE2o8mNh7JsspkzughEoGv"
		}
	}`)
	blockResponse := protocol.NewHttpUpstreamResponse("1", blockBody, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(slotResponse).Once()
	connector.On("SendRequest", ctx, mock.Anything).Return(maxBlocksResponse).Once()
	connector.On("SendRequest", ctx, mock.Anything).Return(blockResponse).Once()

	block, err := specific.SolanaChainSpecific.GetLatestBlock(ctx, connector)

	connector.AssertExpectations(t)
	assert.Nil(t, err)
	assert.Equal(t, uint64(305814139), block.BlockData.Height)
	assert.Equal(t, uint64(327557752), block.BlockData.Slot)
	assert.Equal(t, "7QbMXETjcbRHTLxqAEH62nGE2o8mNh7JsspkzughEoGv", block.BlockData.Hash)
}

func TestSolanaGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := specific.SolanaChainSpecific.GetLatestBlock(ctx, connector)

	connector.AssertExpectations(t)
	assert.Nil(t, block)

	var upErr *protocol.ResponseError
	ok := errors.As(err, &upErr)
	assert.True(t, ok)

	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}
