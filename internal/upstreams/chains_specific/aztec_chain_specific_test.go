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

func TestAztecSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewAztecChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "aztec does not support websocket subscriptions")
}

func TestAztecParseSubscriptionBlock(t *testing.T) {
	block, err := test_utils.NewAztecChainSpecific(context.Background(), nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "aztec does not support websocket subscriptions")
}

func TestAztecParseBlock(t *testing.T) {
	body := []byte(`{
		"proposed":     {"number": 100, "hash": "0xaaaa"},
		"proven":       {"number": 99,  "hash": "0xbbbb"},
		"finalized":    {"number": 98,  "hash": "0xcccc"},
		"checkpointed": {"number": 98,  "hash": "0xdddd"}
	}`)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), nil).ParseBlock(body)
	assert.Nil(t, err)

	expected := protocol.NewBlock(100, 0, blockchain.NewHashIdFromString("0xaaaa"), blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}

func TestAztecParseBlockV4Nested(t *testing.T) {
	// Aztec v4 uses nested block shape for proven/finalized/checkpointed.
	body := []byte(`{
		"proposed":     {"number": 200, "hash": "0x1111"},
		"proven":       {"block": {"number": 199, "hash": "0x2222"}},
		"finalized":    {"block": {"number": 198, "hash": "0x3333"}},
		"checkpointed": {"block": {"number": 198, "hash": "0x4444"}}
	}`)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), nil).ParseBlock(body)
	assert.Nil(t, err)

	expected := protocol.NewBlock(200, 0, blockchain.NewHashIdFromString("0x1111"), blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}

func TestAztecParseBlockInvalidJSON(t *testing.T) {
	block, err := test_utils.NewAztecChainSpecific(context.Background(), nil).ParseBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the aztec L2 tips")
}

func TestAztecParseBlockZeroProposedNumber(t *testing.T) {
	body := []byte(`{
		"proposed": {"number": 0, "hash": "0xaaaa"},
		"proven":   {"number": 0, "hash": "0xbbbb"}
	}`)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), nil).ParseBlock(body)
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the aztec L2 tips")
}

func TestAztecGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
		"jsonrpc": "2.0",
		"result": {
			"proposed":     {"number": 500, "hash": "0xfeed"},
			"proven":       {"number": 499, "hash": "0xbeef"},
			"finalized":    {"number": 498, "hash": "0xdead"},
			"checkpointed": {"number": 498, "hash": "0xcafe"}
		}
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	expected := protocol.NewBlock(500, 0, blockchain.NewHashIdFromString("0xfeed"), blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}

func TestAztecGetLatestBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "rpc error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), connector).GetLatestBlock(ctx)

	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "rpc error", upErr.Message)
}

func TestAztecGetFinalizedBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
		"jsonrpc": "2.0",
		"result": {
			"proposed":  {"number": 500, "hash": "0xfeed"},
			"proven":    {"number": 499, "hash": "0xbeef"},
			"finalized": {"number": 498, "hash": "0xdead"}
		}
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), connector).GetFinalizedBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	expected := protocol.NewBlock(498, 0, blockchain.NewHashIdFromString("0xdead"), blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}

func TestAztecGetFinalizedBlockFallbackToProven(t *testing.T) {
	// When finalized.Number == 0, proven should be used.
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
		"jsonrpc": "2.0",
		"result": {
			"proposed":  {"number": 500, "hash": "0xfeed"},
			"proven":    {"number": 499, "hash": "0xbeef"},
			"finalized": {"number": 0, "hash": "0x0000"}
		}
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), connector).GetFinalizedBlock(ctx)
	assert.Nil(t, err)

	connector.AssertExpectations(t)

	expected := protocol.NewBlock(499, 0, blockchain.NewHashIdFromString("0xbeef"), blockchain.EmptyHash)
	assert.Equal(t, expected, block)
}

func TestAztecGetFinalizedBlockZeroWhenBothZero(t *testing.T) {
	// When both finalized and proven are 0, a ZeroBlock should be returned.
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	body := []byte(`{
		"jsonrpc": "2.0",
		"result": {
			"proposed":  {"number": 500, "hash": "0xfeed"},
			"proven":    {"number": 0,   "hash": "0x0000"},
			"finalized": {"number": 0,   "hash": "0x0000"}
		}
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), connector).GetFinalizedBlock(ctx)
	assert.Nil(t, err)
	assert.True(t, block.IsFullEmpty())

	connector.AssertExpectations(t)
}

func TestAztecGetFinalizedBlockWithError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(2, "finalized error", nil))

	connector.On("SendRequest", ctx, mock.Anything).Return(response)

	block, err := test_utils.NewAztecChainSpecific(context.Background(), connector).GetFinalizedBlock(ctx)

	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, 2, upErr.Code)
	assert.Equal(t, "finalized error", upErr.Message)
}
