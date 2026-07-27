package bitcoin_specific_test

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

const (
	bestBlockHash = "00000000000000000000d7c68a3b5e0794da056f7996c668620eb2b53591a8cf"
	prevBlockHash = "0000000000000000000000000000000000000000000000000000000000003f2f"
)

func methodIs(method string) any {
	return mock.MatchedBy(func(r protocol.RequestHolder) bool { return r.Method() == method })
}

func headerFixture(hash, prev string) []byte {
	return []byte(`{
		"hash": "` + hash + `",
		"confirmations": 1,
		"height": 958407,
		"version": 537427968,
		"time": 1784290128,
		"mediantime": 1784286851,
		"nTx": 3011,
		"previousblockhash": "` + prev + `"
	}`)
}

// jsonRpcEnvelope wraps a raw JSON-RPC "result" payload the way a real
// bitcoind response is shaped: {"result":<payload>,"error":null,"id":1}.
// bitcoind always sends an explicit "error":null on success (unlike the
// other JSON-RPC 2.0 upstreams in this codebase, which omit "error"
// entirely) - see the parse_response.go fix that makes a null error parse
// as "no error" instead of a failure.
func jsonRpcEnvelope(result string) []byte {
	return []byte(`{"result":` + result + `,"error":null,"id":1}`)
}

func TestBitcoinSubscribeHeadRequestUnsupported(t *testing.T) {
	req, err := test_utils.NewBitcoinChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "bitcoin: head subscriptions are not supported")
}

func TestBitcoinParseSubscriptionBlock(t *testing.T) {
	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "bitcoin: head subscriptions are not supported")
}

func TestBitcoinParseBlockHeader(t *testing.T) {
	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), nil).ParseBlock(headerFixture(bestBlockHash, prevBlockHash))
	assert.Nil(t, err)

	expected := protocol.NewBlock(
		958407, 0,
		blockchain.NewHashIdFromString(bestBlockHash),
		blockchain.NewHashIdFromString(prevBlockHash),
	)
	assert.Equal(t, expected, block)
}

func TestBitcoinParseBlockInvalidJSON(t *testing.T) {
	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), nil).ParseBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the bitcoin block header")
}

func TestBitcoinParseBlockEmptyHash(t *testing.T) {
	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), nil).ParseBlock([]byte(`{"height": 100}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the bitcoin block header")
}

func TestBitcoinGetLatestBlock(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	hashResp := protocol.NewHttpUpstreamResponse("1", jsonRpcEnvelope(`"`+bestBlockHash+`"`), 200, protocol.JsonRpc)

	headerResp := protocol.NewHttpUpstreamResponse("1", jsonRpcEnvelope(string(headerFixture(bestBlockHash, prevBlockHash))), 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, methodIs("getbestblockhash")).Return(hashResp).Once()
	connector.On("SendRequest", ctx, methodIs("getblockheader")).Return(headerResp).Once()

	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	assert.Nil(t, err)
	connector.AssertExpectations(t)

	expected := protocol.NewBlock(
		958407, 0,
		blockchain.NewHashIdFromString(bestBlockHash),
		blockchain.NewHashIdFromString(prevBlockHash),
	)
	assert.Equal(t, expected, block)
}

func TestBitcoinGetLatestBlockBestHashError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "best hash error", nil))

	connector.On("SendRequest", ctx, methodIs("getbestblockhash")).Return(response).Once()

	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "best hash error", upErr.Message)
}

func TestBitcoinGetLatestBlockHeaderError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	hashResp := protocol.NewHttpUpstreamResponse("1", jsonRpcEnvelope(`"`+bestBlockHash+`"`), 200, protocol.JsonRpc)
	headerErr := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(2, "header error", nil))

	connector.On("SendRequest", ctx, methodIs("getbestblockhash")).Return(hashResp).Once()
	connector.On("SendRequest", ctx, methodIs("getblockheader")).Return(headerErr).Once()

	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, 2, upErr.Code)
	assert.Equal(t, "header error", upErr.Message)
}

// TestBitcoinGetFinalizedBlockDelegatesToLatest mirrors algorand: bitcoind has
// no separate finalized-head concept in scope here, so GetFinalizedBlock just
// forwards to GetLatestBlock.
func TestBitcoinGetFinalizedBlockDelegatesToLatest(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	hashResp := protocol.NewHttpUpstreamResponse("1", jsonRpcEnvelope(`"`+bestBlockHash+`"`), 200, protocol.JsonRpc)
	headerResp := protocol.NewHttpUpstreamResponse("1", jsonRpcEnvelope(string(headerFixture(bestBlockHash, prevBlockHash))), 200, protocol.JsonRpc)

	connector.On("SendRequest", ctx, methodIs("getbestblockhash")).Return(hashResp).Once()
	connector.On("SendRequest", ctx, methodIs("getblockheader")).Return(headerResp).Once()

	block, err := test_utils.NewBitcoinChainSpecific(context.Background(), connector).GetFinalizedBlock(ctx)
	assert.Nil(t, err)
	connector.AssertExpectations(t)

	expected := protocol.NewBlock(
		958407, 0,
		blockchain.NewHashIdFromString(bestBlockHash),
		blockchain.NewHashIdFromString(prevBlockHash),
	)
	assert.Equal(t, expected, block)
}
