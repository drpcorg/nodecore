package specific_test

import (
	"context"
	"errors"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAlgorandSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewAlgorandChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "algorand does not support websocket subscriptions")
}

func TestAlgorandParseSubscriptionBlock(t *testing.T) {
	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "algorand does not support websocket subscriptions")
}

func TestAlgorandParseBlock(t *testing.T) {
	body := []byte(`{
		"last-round": 12345,
		"catchup-time": 0,
		"stopped-at-unsupported-round": false
	}`)

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), nil).ParseBlock(body)
	assert.Nil(t, err)

	expected := protocol.NewBlock(12345, 0, blockchain.EmptyHash, blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}

func TestAlgorandParseBlockInvalidJSON(t *testing.T) {
	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), nil).ParseBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the algorand status")
}

func TestAlgorandParseBlockZeroLastRound(t *testing.T) {
	body := []byte(`{"last-round": 0}`)

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), nil).ParseBlock(body)
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the algorand status")
}

func TestAlgorandGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
		"last-round": 99999,
		"catchup-time": 0,
		"stopped-at-unsupported-round": false
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.Rest)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	expected := protocol.NewBlock(99999, 0, blockchain.EmptyHash, blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}

func TestAlgorandGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetLatestBlock(ctx)

	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}

func TestAlgorandGetFinalizedBlockDelegatesToLatest(t *testing.T) {
	// GetFinalizedBlock delegates to GetLatestBlock for Algorand.
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
		"last-round": 77777,
		"catchup-time": 0,
		"stopped-at-unsupported-round": false
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.Rest)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetFinalizedBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	expected := protocol.NewBlock(77777, 0, blockchain.EmptyHash, blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}
