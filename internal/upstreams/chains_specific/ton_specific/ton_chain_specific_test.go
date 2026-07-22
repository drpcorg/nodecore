package ton_specific_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/ton_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func tonTestOptions() *chains.Options {
	return &chains.Options{
		InternalTimeout:         5 * time.Second,
		ValidationInterval:      10 * time.Second,
		DisableChainValidation:  new(false),
		DisableHealthValidation: new(false),
	}
}

func newV2(connector connectors.ApiConnector) *ton_specific.TonV2ChainSpecificObject {
	return ton_specific.NewTonV2ChainSpecificObject(context.Background(), chains.GetChain("ton"), "id", connector, tonTestOptions())
}

func newV3(connector connectors.ApiConnector) *ton_specific.TonV3ChainSpecificObject {
	return ton_specific.NewTonV3ChainSpecificObject(context.Background(), chains.GetChain("ton"), "id", connector, tonTestOptions())
}

func TestTonFactoryReturnsV3ForRestIndexer(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestIndexer)
	obj := ton_specific.NewTonChainSpecificObject(
		context.Background(), chains.GetChain("ton"), "id", connector, []connectors.ApiConnector{connector}, tonTestOptions(),
	)
	assert.IsType(t, &ton_specific.TonV3ChainSpecificObject{}, obj)
}

func TestTonFactoryReturnsV2ForRest(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	obj := ton_specific.NewTonChainSpecificObject(
		context.Background(), chains.GetChain("ton"), "id", connector, []connectors.ApiConnector{connector}, tonTestOptions(),
	)
	assert.IsType(t, &ton_specific.TonV2ChainSpecificObject{}, obj)
}

func TestTonSubscribeHeadRequest(t *testing.T) {
	req, err := newV2(nil).SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "ton: head subscriptions are not supported")
}

func TestTonParseSubscriptionBlock(t *testing.T) {
	block, err := newV3(nil).ParseSubscriptionBlock([]byte(`{}`))
	assert.True(t, block.IsFullEmpty())
	assert.EqualError(t, err, "ton: head subscriptions are not supported")
}

func TestTonCapDetectorsAreNil(t *testing.T) {
	assert.Nil(t, newV2(nil).CapDetectors(caps.DetectorInput{}))
	assert.Nil(t, newV3(nil).CapDetectors(caps.DetectorInput{}))
}

func TestTonBlockAndLowerBoundProcessorsAreNil(t *testing.T) {
	assert.Nil(t, newV2(nil).BlockProcessor())
	assert.Nil(t, newV2(nil).LowerBoundProcessor())
	assert.Nil(t, newV3(nil).BlockProcessor())
	assert.Nil(t, newV3(nil).LowerBoundProcessor())
}

func TestTonV2ParseBlock(t *testing.T) {
	body := []byte(`{"ok":true,"result":{"last":{"seqno":48477822,"root_hash":"m2QMxn/1H2Iqm+2wjB3edxNa/rvL9V7bU6MMSPmSfW0="}}}`)

	block, err := newV2(nil).ParseBlock(body)
	require.NoError(t, err)

	expected := protocol.NewBlock(
		48477822,
		0,
		blockchain.NewHashIdFromString("m2QMxn/1H2Iqm+2wjB3edxNa/rvL9V7bU6MMSPmSfW0="),
		blockchain.EmptyHash,
	)
	assert.Equal(t, expected, block)
}

func TestTonV2ParseBlockNotOk(t *testing.T) {
	block, err := newV2(nil).ParseBlock([]byte(`{"ok":false,"error":"boom"}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the ton masterchain info")
}

func TestTonV3ParseBlock(t *testing.T) {
	body := []byte(`{"last":{"seqno":48477822,"root_hash":"m2QMxn/1H2Iqm+2wjB3edxNa/rvL9V7bU6MMSPmSfW0="}}`)

	block, err := newV3(nil).ParseBlock(body)
	require.NoError(t, err)
	assert.Equal(t, uint64(48477822), block.Height)
}

func TestTonV3ParseBlockNoHash(t *testing.T) {
	block, err := newV3(nil).ParseBlock([]byte(`{"last":{"seqno":1}}`))
	assert.True(t, block.IsFullEmpty())
	assert.ErrorContains(t, err, "couldn't parse the ton v3 masterchain info")
}

func matchRestRequest(path string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return strings.Contains(req.Method(), path)
	}
}

func TestTonV2GetLatestBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestConnector)
	body := []byte(`{"ok":true,"result":{"last":{"seqno":48477822,"root_hash":"m2QMxn/1H2Iqm+2wjB3edxNa/rvL9V7bU6MMSPmSfW0="}}}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchRestRequest("/getMasterchainInfo"))).
		Return(protocol.NewHttpUpstreamResponse("1", body, 200, protocol.Rest))

	block, err := newV2(connector).GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(48477822), block.Height)
	connector.AssertExpectations(t)
}

func TestTonV3GetLatestBlock(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.RestIndexer)
	body := []byte(`{"last":{"seqno":48477822,"root_hash":"m2QMxn/1H2Iqm+2wjB3edxNa/rvL9V7bU6MMSPmSfW0="}}`)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchRestRequest("/api/v3/masterchainInfo"))).
		Return(protocol.NewHttpUpstreamResponse("1", body, 200, protocol.Rest))

	block, err := newV3(connector).GetLatestBlock(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(48477822), block.Height)
	connector.AssertExpectations(t)
}
