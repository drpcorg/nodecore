package near_specific_test

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

func TestNearSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewNearChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "near: head subscriptions are not supported")
}

func TestNearParseSubscriptionBlock(t *testing.T) {
	block, err := test_utils.NewNearChainSpecific(context.Background(), nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "near: head subscriptions are not supported")
}

func TestNearParseBlock(t *testing.T) {
	body := []byte(`{
		"author": "validator.near",
		"header": {"height": 207365026, "hash": "9nEcHpjcsfjMwHzHYzDLZeLBEbeqbNRew7oXCSFvi2Wa", "prev_hash": "5qJoxdRBSDaZmGuLzjWfDGnWqCvHdRPuJqkyZv7QwXvJ"},
		"chunks": []
	}`)

	block, err := test_utils.NewNearChainSpecific(context.Background(), nil).ParseBlock(body)
	require.NoError(t, err)

	expected := protocol.NewBlock(
		207365026,
		0,
		blockchain.NewHashIdFromString("9nEcHpjcsfjMwHzHYzDLZeLBEbeqbNRew7oXCSFvi2Wa"),
		blockchain.NewHashIdFromString("5qJoxdRBSDaZmGuLzjWfDGnWqCvHdRPuJqkyZv7QwXvJ"),
	)
	assert.Equal(t, expected, block)
}

func TestNearParseBlockNoPrevHash(t *testing.T) {
	body := []byte(`{"header": {"height": 1, "hash": "9nEcHpjcsfjMwHzHYzDLZeLBEbeqbNRew7oXCSFvi2Wa"}}`)

	block, err := test_utils.NewNearChainSpecific(context.Background(), nil).ParseBlock(body)
	require.NoError(t, err)
	assert.Equal(t, blockchain.EmptyHash, block.ParentHash)
}

func TestNearParseBlockInvalidJSON(t *testing.T) {
	block, err := test_utils.NewNearChainSpecific(context.Background(), nil).ParseBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the near block")
}

func TestNearParseBlockNoHash(t *testing.T) {
	block, err := test_utils.NewNearChainSpecific(context.Background(), nil).ParseBlock([]byte(`{"header":{"height":1}}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the near block")
}

func matchBlockRequestWithFinality(finality string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != "block" {
			return false
		}
		body, err := req.Body()
		if err != nil {
			return false
		}
		return strings.Contains(string(body), `"finality":"`+finality+`"`)
	}
}

func TestNearGetLatestBlockPollsOptimistic(t *testing.T) {
	connector := mocks.NewConnectorMock()
	body := []byte(`{"header": {"height": 207365026, "hash": "9nEcHpjcsfjMwHzHYzDLZeLBEbeqbNRew7oXCSFvi2Wa", "prev_hash": "5qJoxdRBSDaZmGuLzjWfDGnWqCvHdRPuJqkyZv7QwXvJ"}}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchBlockRequestWithFinality("optimistic"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", body, protocol.JsonRpc))

	block, err := test_utils.NewNearChainSpecific(context.Background(), connector).GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(207365026), block.Height)
	connector.AssertExpectations(t)
}

func TestNearGetFinalizedBlockPollsFinal(t *testing.T) {
	connector := mocks.NewConnectorMock()
	body := []byte(`{"header": {"height": 207365020, "hash": "5qJoxdRBSDaZmGuLzjWfDGnWqCvHdRPuJqkyZv7QwXvJ", "prev_hash": "9nEcHpjcsfjMwHzHYzDLZeLBEbeqbNRew7oXCSFvi2Wa"}}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchBlockRequestWithFinality("final"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", body, protocol.JsonRpc))

	block, err := test_utils.NewNearChainSpecific(context.Background(), connector).GetFinalizedBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(207365020), block.Height)
	connector.AssertExpectations(t)
}

func TestNearGetLatestBlockError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	block, err := test_utils.NewNearChainSpecific(context.Background(), connector).GetLatestBlock(context.Background())
	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
}

func TestNearBlockProcessorIsCreated(t *testing.T) {
	processor := test_utils.NewNearChainSpecific(context.Background(), mocks.NewConnectorMock()).BlockProcessor()
	assert.NotNil(t, processor)
}

func TestNearCapDetectorsAreNil(t *testing.T) {
	detectors := test_utils.NewNearChainSpecific(context.Background(), nil).CapDetectors(caps.DetectorInput{})
	assert.Nil(t, detectors)
}
