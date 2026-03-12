package specific_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSolanaSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewSolanaChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, err)

	body, err := req.Body()

	assert.Nil(t, err)
	assert.Equal(t, "1", req.Id())
	assert.Equal(t, "slotSubscribe", req.Method())
	assert.False(t, req.IsStream())
	require.JSONEq(t, `{"id":"1","jsonrpc":"2.0","method":"slotSubscribe","params":null}`, string(body))
}

func TestSolanaParseSubBlockErrEpochInfo(t *testing.T) {
	connector := mocks.NewConnectorMock()
	body := []byte(`{
            "jsonrpc": "2.0",
            "error": {
                "code": -32000,
                "message": "Server error: EpochInfo"
            },
            "id": 1
	}`)
	slot := []byte(`{
            "slot": 405220706,
            "parent": 405220705,
            "root": 405220674
	}`)
	epochResponse := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(epochResponse)

	block, err := test_utils.NewSolanaChainSpecific(context.Background(), connector).ParseSubscriptionBlock(slot)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	hash, parentHash := specific.SyntheticHashes(405220706, 405220705)
	blockData := protocol.NewBlockData(405220706, 405220706, hash, parentHash)
	assert.Equal(t, blockData, block.BlockData)
}

func TestSolanaParseSubBLock(t *testing.T) {
	connector := mocks.NewConnectorMock()
	solanaSpecific := test_utils.NewSolanaChainSpecific(context.Background(), connector)
	body := []byte(`{
            "slot": 405220706,
            "parent": 405220705,
            "root": 405220674
	}`)
	body1 := []byte(`{
            "slot": 405219989,
            "parent": 405220705,
            "root": 405220674
	}`)
	epochBody := []byte(`{
		"jsonrpc": "2.0",
		"result": {
			"absoluteSlot": 405219988,
			"blockHeight": 383325939,
			"epoch": 938,
			"slotIndex": 3988,
			"slotsInEpoch": 432000,
			"transactionCount": 494578437235
		},
		"id": 1
	}`)
	epochResponse := protocol.NewHttpUpstreamResponse("1", epochBody, 200, protocol.JsonRpc)

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(epochResponse)

	block, err := solanaSpecific.ParseSubscriptionBlock(body)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	hash, parentHash := specific.SyntheticHashes(405219988, 405219987)
	blockData := protocol.NewBlockData(383325939, 405219988, hash, parentHash)
	assert.Equal(t, blockData, block.BlockData)

	block, err = solanaSpecific.ParseSubscriptionBlock(body1)
	assert.Nil(t, err)

	hash, parentHash = specific.SyntheticHashes(405219989, 405219988)
	blockData = protocol.NewBlockData(383325940, 405219989, hash, parentHash)
	assert.Equal(t, blockData, block.BlockData)

	connector.AssertNumberOfCalls(t, "SendRequest", 1)
}

func TestSolanaGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	epochBody := []byte(`{
		"jsonrpc": "2.0",
		"result": {
			"absoluteSlot": 405219988,
			"blockHeight": 383325939,
			"epoch": 938,
			"slotIndex": 3988,
			"slotsInEpoch": 432000,
			"transactionCount": 494578437235
		},
		"id": 1
	}`)
	epochResponse := protocol.NewHttpUpstreamResponse("1", epochBody, 200, protocol.JsonRpc)

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(epochResponse)

	block, err := test_utils.NewSolanaChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	hash, parentHash := specific.SyntheticHashes(405219988, 405219987)
	blockData := protocol.NewBlockData(383325939, 405219988, hash, parentHash)

	assert.Equal(t, blockData, block.BlockData)
}

func TestSolanaGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(response)

	block, err := test_utils.NewSolanaChainSpecific(context.Background(), connector).GetLatestBlock(ctx)

	connector.AssertExpectations(t)
	assert.Nil(t, block)

	var upErr *protocol.ResponseError
	ok := errors.As(err, &upErr)
	assert.True(t, ok)

	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}
