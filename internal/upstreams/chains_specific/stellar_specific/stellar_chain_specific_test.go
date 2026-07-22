package stellar_specific_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/stellar_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func stellarTestOptions() *chains.Options {
	return &chains.Options{
		InternalTimeout:         5 * time.Second,
		ValidationInterval:      10 * time.Second,
		DisableChainValidation:  new(false),
		DisableHealthValidation: new(false),
	}
}

func newRpc(connector connectors.ApiConnector) *stellar_specific.StellarRpcChainSpecificObject {
	return stellar_specific.NewStellarRpcChainSpecificObject(context.Background(), chains.GetChain("stellar"), "id", connector, time.Second, stellarTestOptions())
}

func newHorizon(connector connectors.ApiConnector) *stellar_specific.StellarHorizonChainSpecificObject {
	return stellar_specific.NewStellarHorizonChainSpecificObject(context.Background(), chains.GetChain("stellar"), "id", connector, time.Second, stellarTestOptions())
}

func TestStellarFactoryReturnsHorizonForRest(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	obj := stellar_specific.NewStellarChainSpecificObject(
		context.Background(), chains.GetChain("stellar"), "id", connector, []connectors.ApiConnector{connector}, time.Second, stellarTestOptions(),
	)
	assert.IsType(t, &stellar_specific.StellarHorizonChainSpecificObject{}, obj)
}

func TestStellarFactoryReturnsRpcForJsonRpc(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.JsonRpcConnector)
	obj := stellar_specific.NewStellarChainSpecificObject(
		context.Background(), chains.GetChain("stellar"), "id", connector, []connectors.ApiConnector{connector}, time.Second, stellarTestOptions(),
	)
	assert.IsType(t, &stellar_specific.StellarRpcChainSpecificObject{}, obj)
}

func TestStellarSubscribeHeadRequest(t *testing.T) {
	req, err := newRpc(nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "stellar: head subscriptions are not supported")
}

func TestStellarParseSubscriptionBlock(t *testing.T) {
	block, err := newHorizon(nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "stellar: head subscriptions are not supported")
}

func TestStellarCapDetectorsAreNil(t *testing.T) {
	assert.Nil(t, newRpc(nil).CapDetectors(caps.DetectorInput{}))
	assert.Nil(t, newHorizon(nil).CapDetectors(caps.DetectorInput{}))
}

func TestStellarBlockProcessorsAreCreated(t *testing.T) {
	assert.NotNil(t, newRpc(mocks.NewConnectorMock()).BlockProcessor())
	assert.NotNil(t, newHorizon(mocks.NewConnectorMockWithType(specs.RestConnector)).BlockProcessor())
}

func TestStellarRpcParseBlock(t *testing.T) {
	body := []byte(`{"id":"906c437021a1a3f0e4a46e11856ba65a4409d1c0b8f4bfc03f1c34acd0d19367","sequence":63525714,"protocolVersion":23}`)

	block, err := newRpc(nil).ParseBlock(body)
	require.NoError(t, err)

	expected := protocol.NewBlock(
		63525714,
		0,
		blockchain.NewHashIdFromString("906c437021a1a3f0e4a46e11856ba65a4409d1c0b8f4bfc03f1c34acd0d19367"),
		blockchain.EmptyHash,
	)
	assert.Equal(t, expected, block)
}

func TestStellarRpcParseBlockNoId(t *testing.T) {
	block, err := newRpc(nil).ParseBlock([]byte(`{"sequence":1}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the stellar latest ledger")
}

func TestStellarHorizonParseBlock(t *testing.T) {
	body := []byte(`{"_embedded":{"records":[{"hash":"abc123","prev_hash":"def456","sequence":58387248}]}}`)

	block, err := newHorizon(nil).ParseBlock(body)
	require.NoError(t, err)
	assert.Equal(t, uint64(58387248), block.Height)
	assert.Equal(t, blockchain.NewHashIdFromString("def456"), block.ParentHash)
}

func TestStellarHorizonParseBlockEmptyPage(t *testing.T) {
	block, err := newHorizon(nil).ParseBlock([]byte(`{"_embedded":{"records":[]}}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the horizon ledgers page")
}

func TestStellarRpcGetLatestBlock(t *testing.T) {
	connector := mocks.NewConnectorMock()
	body := []byte(`{"id":"906c437021a1a3f0e4a46e11856ba65a4409d1c0b8f4bfc03f1c34acd0d19367","sequence":63525714}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "getLatestLedger"
	})).Return(protocol.NewSimpleHttpUpstreamResponse("1", body, protocol.JsonRpc))

	block, err := newRpc(connector).GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(63525714), block.Height)
	connector.AssertExpectations(t)
}

func TestStellarHorizonGetLatestBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	body := []byte(`{"_embedded":{"records":[{"hash":"abc123","prev_hash":"def456","sequence":58387248}]}}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(req protocol.RequestHolder) bool {
		return req.Method() == "GET#/ledgers"
	})).Return(protocol.NewHttpUpstreamResponse("1", body, 200, protocol.Rest))

	block, err := newHorizon(connector).GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(58387248), block.Height)
	connector.AssertExpectations(t)
}
