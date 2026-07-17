package beacon_specific_test

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

const headHeaderBody = `{
  "execution_optimistic": false,
  "finalized": false,
  "data": {
    "root": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "canonical": true,
    "header": {
      "message": {
        "slot": "7654321",
        "proposer_index": "12345",
        "parent_root": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        "state_root": "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
        "body_root": "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
      },
      "signature": "0xee"
    }
  }
}`

func TestBeaconBlockProcessorEnabledForFinalized(t *testing.T) {
	// Beacon reuses the eth-like processor to track the finalized checkpoint, so
	// it must be non-nil (unlike the other REST families that return nil).
	assert.NotNil(t, test_utils.NewBeaconChainSpecific(context.Background(), nil).BlockProcessor())
}

func TestBeaconSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewBeaconChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "beacon chain does not support websocket subscriptions")
}

func TestBeaconParseSubscriptionBlock(t *testing.T) {
	block, err := test_utils.NewBeaconChainSpecific(context.Background(), nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "beacon chain does not support websocket subscriptions")
}

func TestBeaconParseBlock(t *testing.T) {
	block, err := test_utils.NewBeaconChainSpecific(context.Background(), nil).ParseBlock([]byte(headHeaderBody))
	assert.NoError(t, err)

	expected := protocol.NewBlock(
		7654321,
		0,
		blockchain.NewHashIdFromString("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		blockchain.NewHashIdFromString("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
	)
	assert.Equal(t, expected, block)
}

func TestBeaconParseBlockInvalidJSON(t *testing.T) {
	block, err := test_utils.NewBeaconChainSpecific(context.Background(), nil).ParseBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the beacon header")
}

func TestBeaconParseBlockNoSlot(t *testing.T) {
	block, err := test_utils.NewBeaconChainSpecific(context.Background(), nil).ParseBlock([]byte(`{"data":{"root":"0xaa"}}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the beacon header")
}

func TestBeaconGetLatestBlockPollsHeadHeader(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	resp := protocol.NewHttpUpstreamResponse("1", []byte(headHeaderBody), 200, protocol.Rest)
	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		rp := r.RequestParams()
		return r.Method() == "GET#/eth/v1/beacon/headers/*" &&
			rp != nil && len(rp.PathParams) == 1 && rp.PathParams[0] == "head"
	})).Return(resp).Once()

	block, err := test_utils.NewBeaconChainSpecific(ctx, connector).GetLatestBlock(ctx)
	connector.AssertExpectations(t)
	assert.NoError(t, err)
	assert.Equal(t, uint64(7654321), block.Height)
	assert.Equal(t, uint64(0), block.Slot)
}

func TestBeaconGetFinalizedBlockUsesFinalizedId(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()

	resp := protocol.NewHttpUpstreamResponse("1", []byte(headHeaderBody), 200, protocol.Rest)
	connector.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		rp := r.RequestParams()
		return r.Method() == "GET#/eth/v1/beacon/headers/*" &&
			rp != nil && len(rp.PathParams) == 1 && rp.PathParams[0] == "finalized"
	})).Return(resp).Once()

	_, err := test_utils.NewBeaconChainSpecific(ctx, connector).GetFinalizedBlock(ctx)
	connector.AssertExpectations(t)
	assert.NoError(t, err)
}

func TestBeaconGetLatestBlockError(t *testing.T) {
	ctx := context.Background()
	connector := mocks.NewConnectorMock()
	resp := protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil))
	connector.On("SendRequest", ctx, mock.Anything).Return(resp).Once()

	block, err := test_utils.NewBeaconChainSpecific(ctx, connector).GetLatestBlock(ctx)
	connector.AssertExpectations(t)
	assert.True(t, block.IsFullEmpty())

	var upErr *protocol.ResponseError
	assert.True(t, errors.As(err, &upErr))
	assert.Equal(t, "boom", upErr.Message)
}
