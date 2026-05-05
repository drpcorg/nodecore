package specific_test

import (
	"context"
	"encoding/base64"
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

// TestAlgorandGetLatestBlockUsesBlockHeader covers the head poll path that
// derives Hash from `seed` and ParentHash from `prev` returned by
// /v2/blocks/{round}?header-only=true. algod stores both as base64-encoded
// 32-byte values.
func TestAlgorandGetLatestBlockUsesBlockHeader(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	statusBody := []byte(`{
		"last-round": 99999,
		"catchup-time": 0,
		"stopped-at-unsupported-round": false
	}`)
	statusResp := protocol.NewHttpUpstreamResponse("1", statusBody, 200, protocol.Rest)

	seedBytes := bytes32(0xAA)
	prevBytes := bytes32(0xBB)
	blockBody := []byte(`{
		"block": {
			"rnd": 99999,
			"seed": "` + base64.StdEncoding.EncodeToString(seedBytes) + `",
			"prev": "` + base64.StdEncoding.EncodeToString(prevBytes) + `"
		}
	}`)
	blockResp := protocol.NewHttpUpstreamResponse("1", blockBody, 200, protocol.Rest)

	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/status"
	})).Return(statusResp).Once()
	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/blocks/99999?header-only=true"
	})).Return(blockResp).Once()

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	assert.Nil(t, err)
	connector.AssertExpectations(t)

	expected := protocol.NewBlock(
		99999, 0,
		blockchain.NewHashIdFromBytes(seedBytes),
		blockchain.NewHashIdFromBytes(prevBytes),
	)
	assert.Equal(t, expected, block)
}

// TestAlgorandGetLatestBlockFallsBackToRoundBytes covers the case where the
// block header is reachable but `seed`/`prev` are missing; the implementation
// must produce a deterministic 32-byte HashId so consumers never see an empty
// BlockId/ParentBlockId in the HeadEvent.
func TestAlgorandGetLatestBlockFallsBackToRoundBytes(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	statusBody := []byte(`{"last-round": 99999, "catchup-time": 0}`)
	statusResp := protocol.NewHttpUpstreamResponse("1", statusBody, 200, protocol.Rest)
	blockBody := []byte(`{"block": {"rnd": 99999}}`) // no seed / prev
	blockResp := protocol.NewHttpUpstreamResponse("1", blockBody, 200, protocol.Rest)

	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/status"
	})).Return(statusResp).Once()
	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/blocks/99999?header-only=true"
	})).Return(blockResp).Once()

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	assert.Nil(t, err)
	connector.AssertExpectations(t)

	assert.Equal(t, uint64(99999), block.Height)
	assert.Len(t, []byte(block.Hash), 32, "hash must always be 32 bytes")
	assert.Len(t, []byte(block.ParentHash), 32, "parent hash must always be 32 bytes")
	// Last 4 bytes of the round-encoded fallback carry the round itself
	// (big-endian); parent encodes round-1.
	assert.Equal(t, encodeRound(99999), []byte(block.Hash))
	assert.Equal(t, encodeRound(99998), []byte(block.ParentHash))
}

func TestAlgorandGetLatestBlockStatusError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	response := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "block error", nil))

	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/status"
	})).Return(response).Once()

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, 1, upErr.Code)
	assert.Equal(t, "block error", upErr.Message)
}

func TestAlgorandGetLatestBlockBlockFetchError(t *testing.T) {
	// Status succeeds but the block-header fetch fails. We must surface the
	// error so the head poller skips this tick rather than publishing a
	// HeadEvent with empty hashes.
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	statusBody := []byte(`{"last-round": 42, "catchup-time": 0}`)
	statusResp := protocol.NewHttpUpstreamResponse("1", statusBody, 200, protocol.Rest)
	blockErr := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(2, "boom", nil))

	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/status"
	})).Return(statusResp).Once()
	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/blocks/42?header-only=true"
	})).Return(blockErr).Once()

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetLatestBlock(ctx)
	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't fetch algorand block 42 header")
}

// TestAlgorandGetFinalizedBlockDelegatesToLatest exercises the same code
// path - GetFinalizedBlock simply forwards to GetLatestBlock for Algorand,
// since every committed round is final under pure proof-of-stake.
func TestAlgorandGetFinalizedBlockDelegatesToLatest(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	statusBody := []byte(`{"last-round": 77777, "catchup-time": 0}`)
	statusResp := protocol.NewHttpUpstreamResponse("1", statusBody, 200, protocol.Rest)
	seedBytes := bytes32(0xCC)
	prevBytes := bytes32(0xDD)
	blockBody := []byte(`{
		"block": {
			"rnd": 77777,
			"seed": "` + base64.StdEncoding.EncodeToString(seedBytes) + `",
			"prev": "` + base64.StdEncoding.EncodeToString(prevBytes) + `"
		}
	}`)
	blockResp := protocol.NewHttpUpstreamResponse("1", blockBody, 200, protocol.Rest)

	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/status"
	})).Return(statusResp).Once()
	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/v2/blocks/77777?header-only=true"
	})).Return(blockResp).Once()

	block, err := test_utils.NewAlgorandChainSpecific(context.Background(), connector).GetFinalizedBlock(ctx)
	assert.Nil(t, err)
	connector.AssertExpectations(t)

	expected := protocol.NewBlock(
		77777, 0,
		blockchain.NewHashIdFromBytes(seedBytes),
		blockchain.NewHashIdFromBytes(prevBytes),
	)
	assert.Equal(t, expected, block)
}

func bytes32(fill byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = fill
	}
	return out
}

func encodeRound(round uint64) []byte {
	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		out[31-i] = byte(round & 0xff)
		round >>= 8
	}
	return out
}
