package solana_specific_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
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
	require.JSONEq(t, `{"id":1,"jsonrpc":"2.0","method":"slotSubscribe","params":[]}`, string(body))
}

// A Solana websocket endpoint serves only the pubsub methods, so a head specific built
// from the ws connector must ask getEpochInfo over the upstream's json-rpc connector.
func TestSolanaWsHeadSpecificAsksEpochInfoOverJsonRpc(t *testing.T) {
	wsConnector := mocks.NewConnectorMockWithType(specs.WebsocketConnector)
	httpConnector := mocks.NewConnectorMock()
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
	httpConnector.On("SendRequest", mock.Anything, mock.Anything).Return(protocol.NewHttpUpstreamResponse("1", epochBody, 200, protocol.JsonRpc))
	solanaSpecific := test_utils.NewSolanaChainSpecificWithConnectors(
		context.Background(),
		wsConnector,
		[]connectors.ApiConnector{httpConnector, wsConnector},
	)
	hash, parentHash := specific_helpers.SyntheticHashes(405219988, 405219987)
	expected := protocol.NewBlock(383325939, 405219988, hash, parentHash)

	block, err := solanaSpecific.GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, expected, block)

	block, err = solanaSpecific.ParseSubscriptionBlock([]byte(`{"slot": 405220706, "parent": 405220705, "root": 405220674}`))
	require.NoError(t, err)
	assert.Equal(t, expected, block)

	httpConnector.AssertNumberOfCalls(t, "SendRequest", 2)
	wsConnector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

// Without a single successful getEpochInfo there is no height to estimate from. A slot
// is not a height, so the specific must fail instead of publishing a head built from one.
func TestSolanaParseSubBlockErrEpochInfoWithoutKnownHeight(t *testing.T) {
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

	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	require.ErrorAs(t, err, &upErr)
	assert.Equal(t, -32000, upErr.Code)
	assert.Equal(t, "Server error: EpochInfo", upErr.Message)
}

// Once a height is known, a failing getEpochInfo falls back to the height estimated from
// the slot delta rather than failing the head.
func TestSolanaParseSubBlockErrEpochInfoUsesEstimatedHeight(t *testing.T) {
	connector := mocks.NewConnectorMock()
	solanaSpecific := test_utils.NewSolanaChainSpecific(context.Background(), connector)
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
	errorBody := []byte(`{
            "jsonrpc": "2.0",
            "error": {
                "code": -32000,
                "message": "Server error: EpochInfo"
            },
            "id": 1
	}`)
	connector.On("SendRequest", mock.Anything, mock.Anything).Return(protocol.NewHttpUpstreamResponse("1", epochBody, 200, protocol.JsonRpc)).Once()
	connector.On("SendRequest", mock.Anything, mock.Anything).Return(protocol.NewHttpUpstreamResponse("1", errorBody, 200, protocol.JsonRpc)).Once()

	_, err := solanaSpecific.ParseSubscriptionBlock([]byte(`{"slot": 405219988, "parent": 405219987, "root": 405219900}`))
	require.NoError(t, err)

	// 10 slots later: past the check interval, so getEpochInfo is asked again and fails
	block, err := solanaSpecific.ParseSubscriptionBlock([]byte(`{"slot": 405219998, "parent": 405219997, "root": 405219900}`))
	require.NoError(t, err)

	connector.AssertExpectations(t)
	hash, parentHash := specific_helpers.SyntheticHashes(405219998, 405219997)
	assert.Equal(t, protocol.NewBlock(383325949, 405219998, hash, parentHash), block)
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

	hash, parentHash := specific_helpers.SyntheticHashes(405219988, 405219987)
	blockData := protocol.NewBlock(383325939, 405219988, hash, parentHash)
	assert.Equal(t, blockData, block)

	block, err = solanaSpecific.ParseSubscriptionBlock(body1)
	assert.Nil(t, err)

	hash, parentHash = specific_helpers.SyntheticHashes(405219989, 405219988)
	blockData = protocol.NewBlock(383325940, 405219989, hash, parentHash)
	assert.Equal(t, blockData, block)

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

	hash, parentHash := specific_helpers.SyntheticHashes(405219988, 405219987)
	blockData := protocol.NewBlock(383325939, 405219988, hash, parentHash)

	assert.Equal(t, blockData, block)
}

func TestSolanaGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", mock.Anything, mock.Anything).Return(response)

	block, err := test_utils.NewSolanaChainSpecific(context.Background(), connector).GetLatestBlock(ctx)

	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	ok := errors.As(err, &upErr)
	assert.True(t, ok)

	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}
