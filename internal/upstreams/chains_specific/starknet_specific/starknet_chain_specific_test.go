package starknet_specific_test

import (
	"context"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestStarknetSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewStarknetChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "starknet: head subscriptions are not supported")
}

func TestStarknetParseSubscriptionBlock(t *testing.T) {
	block, err := test_utils.NewStarknetChainSpecific(context.Background(), nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "starknet: head subscriptions are not supported")
}

func TestStarknetParseBlock(t *testing.T) {
	body := []byte(`{
		"status": "ACCEPTED_ON_L2",
		"block_hash": "0x70b9eceeb7d852dd1a474987ef6d1a233459244e78026731516593629ba6ebf",
		"parent_hash": "0x2a2bcca5b0f8705863c2bec2c88329930e309e02e33b08b6bfb02cbb6f14596",
		"block_number": 12262080,
		"timestamp": 1753100000,
		"transactions": []
	}`)

	block, err := test_utils.NewStarknetChainSpecific(context.Background(), nil).ParseBlock(body)
	require.NoError(t, err)

	expected := protocol.NewBlock(
		12262080,
		0,
		blockchain.NewHashIdFromString("0x70b9eceeb7d852dd1a474987ef6d1a233459244e78026731516593629ba6ebf"),
		blockchain.NewHashIdFromString("0x2a2bcca5b0f8705863c2bec2c88329930e309e02e33b08b6bfb02cbb6f14596"),
	)
	assert.Equal(t, expected, block)
}

func TestStarknetParseBlockNoParentHash(t *testing.T) {
	body := []byte(`{"block_hash": "0xabc", "block_number": 0}`)

	block, err := test_utils.NewStarknetChainSpecific(context.Background(), nil).ParseBlock(body)
	require.NoError(t, err)
	assert.Equal(t, blockchain.EmptyHash, block.ParentHash)
}

func TestStarknetParseBlockInvalidJSON(t *testing.T) {
	block, err := test_utils.NewStarknetChainSpecific(context.Background(), nil).ParseBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the starknet block")
}

func TestStarknetParseBlockNoHash(t *testing.T) {
	block, err := test_utils.NewStarknetChainSpecific(context.Background(), nil).ParseBlock([]byte(`{"block_number":1}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the starknet block")
}

func matchBlockRequestWithTag(tag string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != "starknet_getBlockWithTxHashes" {
			return false
		}
		body, err := req.Body()
		if err != nil {
			return false
		}
		return strings.Contains(string(body), `["`+tag+`"]`)
	}
}

func TestStarknetGetLatestBlockPollsLatest(t *testing.T) {
	connector := mocks.NewConnectorMock()
	body := []byte(`{"block_hash": "0xaaa", "parent_hash": "0xbbb", "block_number": 12262080}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchBlockRequestWithTag("latest"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", body, protocol.JsonRpc))

	block, err := test_utils.NewStarknetChainSpecific(context.Background(), connector).GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(12262080), block.Height)
	connector.AssertExpectations(t)
}

func TestStarknetGetFinalizedBlockPollsL1Accepted(t *testing.T) {
	connector := mocks.NewConnectorMock()
	body := []byte(`{"block_hash": "0xccc", "parent_hash": "0xddd", "block_number": 12259300}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchBlockRequestWithTag("l1_accepted"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", body, protocol.JsonRpc))

	block, err := test_utils.NewStarknetChainSpecific(context.Background(), connector).GetFinalizedBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(12259300), block.Height)
	connector.AssertExpectations(t)
}

func TestStarknetGetLatestBlockError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	block, err := test_utils.NewStarknetChainSpecific(context.Background(), connector).GetLatestBlock(context.Background())
	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
}

func TestStarknetBlockProcessorIsCreated(t *testing.T) {
	processor := test_utils.NewStarknetChainSpecific(context.Background(), mocks.NewConnectorMock()).BlockProcessor()
	assert.NotNil(t, processor)
}

func TestStarknetCapDetectorsAreNil(t *testing.T) {
	detectors := test_utils.NewStarknetChainSpecific(context.Background(), nil).CapDetectors(caps.DetectorInput{})
	assert.Nil(t, detectors)
}
