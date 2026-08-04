package polkadot_specific_test

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

const testHeader = `{
	"parentHash": "0x0f5f...11",
	"number": "0x1a2b3c",
	"stateRoot": "0xcc",
	"extrinsicsRoot": "0xdd",
	"digest": {
		"logs": []
	}
}`

func expectHeaderAndHash(connector *mocks.ConnectorMock, hash string) {
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "chain_getHeader"
	})).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(testHeader), protocol.JsonRpc))

	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		if req.Method() != "chain_getBlockHash" {
			return false
		}
		body, err := req.Body()
		return err == nil && strings.Contains(string(body), `["0x1a2b3c"]`)
	})).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"`+hash+`"`), protocol.JsonRpc))
}

// A Polkadot header carries no hash of its own, so the head read is two calls:
// chain_getHeader for height/parentHash, then chain_getBlockHash for the hash.
func TestPolkadotGetLatestBlockResolvesHash(t *testing.T) {
	connector := mocks.NewConnectorMock()
	expectHeaderAndHash(connector, "0xdeadbeef")

	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), connector).
		GetLatestBlock(context.Background())
	require.NoError(t, err)

	assert.Equal(t, uint64(1715004), block.Height)
	assert.Equal(t, blockchain.NewHashIdFromString("0xdeadbeef"), block.Hash)
	assert.Equal(t, blockchain.NewHashIdFromString("0x0f5f...11"), block.ParentHash)
	connector.AssertExpectations(t)
}

func TestPolkadotGetLatestBlockHeaderError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), connector).
		GetLatestBlock(context.Background())
	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
}

// On the poll path a missing hash is a real failure - the poller simply logs and
// retries on the next tick, so there is no reason to publish a partial block.
func TestPolkadotGetLatestBlockHashError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "chain_getHeader"
	})).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(testHeader), protocol.JsonRpc))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "chain_getBlockHash"
	})).Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), connector).
		GetLatestBlock(context.Background())
	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
}

func TestPolkadotParseBlockLeavesHashEmpty(t *testing.T) {
	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), nil).ParseBlock([]byte(testHeader))
	require.NoError(t, err)

	assert.Equal(t, uint64(1715004), block.Height)
	assert.Equal(t, blockchain.EmptyHash, block.Hash)
	assert.Equal(t, blockchain.NewHashIdFromString("0x0f5f...11"), block.ParentHash)
}

func TestPolkadotParseBlockErrors(t *testing.T) {
	for _, body := range []string{`not json`, `{}`, `{"parentHash":"0xaa"}`, `{"number":"0xzz"}`} {
		block, err := test_utils.NewPolkadotChainSpecific(context.Background(), nil).ParseBlock([]byte(body))
		assert.True(t, block.IsFullEmpty(), "body %s should not yield a block", body)
		assert.Error(t, err, "body %s should be rejected", body)
	}
}

func TestPolkadotSubscribeHeadRequest(t *testing.T) {
	req, err := test_utils.NewPolkadotChainSpecific(context.Background(), nil).SubscribeHeadRequest()
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, "chain_subscribeNewHeads", req.Method())
}

func TestPolkadotParseSubscriptionBlockResolvesHash(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "chain_getBlockHash"
	})).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xfeed"`), protocol.JsonRpc))

	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), connector).
		ParseSubscriptionBlock([]byte(testHeader))
	require.NoError(t, err)

	assert.Equal(t, uint64(1715004), block.Height)
	assert.Equal(t, blockchain.NewHashIdFromString("0xfeed"), block.Hash)
}

// A parse error from ParseSubscriptionBlock is terminal in blocks/head.go: it
// returns from the subscription goroutine and the head stalls until the
// no-updates timer resubscribes. So a failed hash lookup degrades to a
// height-only block instead. Safe, because chain-level head selection is
// height-based and never reads Block.Hash.
func TestPolkadotParseSubscriptionBlockDegradesWhenHashFails(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(1, "boom", nil)))

	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), connector).
		ParseSubscriptionBlock([]byte(testHeader))
	require.NoError(t, err)

	assert.Equal(t, uint64(1715004), block.Height)
	assert.Equal(t, blockchain.EmptyHash, block.Hash)
	assert.Equal(t, blockchain.NewHashIdFromString("0x0f5f...11"), block.ParentHash)
}

// An unparseable notification is still an error - there is no height to publish.
func TestPolkadotParseSubscriptionBlockRejectsGarbage(t *testing.T) {
	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), mocks.NewConnectorMock()).
		ParseSubscriptionBlock([]byte(`not json`))
	assert.True(t, block.IsFullEmpty())
	assert.Error(t, err)
}

// Strict dshackle parity: dshackle's polkadot has no finalized head, so nothing
// polls for one and BlockProcessor stays nil.
func TestPolkadotGetFinalizedBlockIsUnsupported(t *testing.T) {
	block, err := test_utils.NewPolkadotChainSpecific(context.Background(), nil).
		GetFinalizedBlock(context.Background())
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "polkadot: finalized block detection is not supported")
}

func TestPolkadotBlockAndLabelsProcessorsAreNil(t *testing.T) {
	specific := test_utils.NewPolkadotChainSpecific(context.Background(), mocks.NewConnectorMock())
	assert.Nil(t, specific.BlockProcessor())
	assert.Nil(t, specific.LabelsProcessor())
}

// WsCap gates subscription serving (flow/matchers.go), and Polkadot has 11
// subscriptions, so the ws presence detector must be present.
func TestPolkadotCapDetectorsIncludeWsPresence(t *testing.T) {
	detectors := test_utils.NewPolkadotChainSpecific(context.Background(), mocks.NewConnectorMock()).
		CapDetectors(caps.DetectorInput{})
	assert.Len(t, detectors, 1)
}

func TestPolkadotLowerBoundProcessorIsCreated(t *testing.T) {
	processor := test_utils.NewPolkadotChainSpecific(context.Background(), mocks.NewConnectorMock()).
		LowerBoundProcessor()
	assert.NotNil(t, processor)
}

// newTestChainOptions sets both flags false, so no system_health probe is registered.
func TestPolkadotHealthValidatorsEmptyWhenChecksDisabled(t *testing.T) {
	validators := test_utils.NewPolkadotChainSpecific(context.Background(), mocks.NewConnectorMock()).
		HealthValidators()
	assert.Empty(t, validators)
}

func TestPolkadotSettingsValidators(t *testing.T) {
	validators := test_utils.NewPolkadotChainSpecific(context.Background(), mocks.NewConnectorMock()).
		SettingsValidators()
	assert.Len(t, validators, 1)
}
