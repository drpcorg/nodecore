package stellar_specific_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func horizonRootResponse(latest, elder string) protocol.ResponseHolder {
	body := `{"horizon_version":"27.0.0-abc","history_latest_ledger":` + latest +
		`,"history_elder_ledger":` + elder +
		`,"network_passphrase":"Public Global Stellar Network ; September 2015"}`
	return protocol.NewHttpUpstreamResponse("1", []byte(body), 200, protocol.Rest)
}

func TestStellarHorizonGetLatestBlockReadsTheRootDocument(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMockWithType(specs.RestConnector)
	conn.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/"
	})).Return(horizonRootResponse("63563999", "63563520")).Once()

	block, err := test_utils.NewStellarHorizonChainSpecific(ctx, conn).GetLatestBlock(ctx)
	require.NoError(t, err)
	conn.AssertExpectations(t)

	hash, parentHash := specific_helpers.SyntheticHashes(63563999, 63563998)
	assert.Equal(t, uint64(63563999), block.Height)
	assert.Equal(t, hash, block.Hash)
	assert.Equal(t, parentHash, block.ParentHash)
}

// Both flavors derive hashes from the sequence the same way, so a pool mixing
// rpc and horizon upstreams sees linkable head hashes for the same ledger.
func TestStellarHorizonAndRpcAgreeOnHashesForTheSameSequence(t *testing.T) {
	ctx := context.Background()

	rpcConn := mocks.NewConnectorMock()
	rpcConn.On("SendRequest", ctx, mock.Anything).Return(healthResponse("777", "1")).Once()
	rpcBlock, err := test_utils.NewStellarRpcChainSpecific(ctx, rpcConn).GetLatestBlock(ctx)
	require.NoError(t, err)

	horizonConn := mocks.NewConnectorMockWithType(specs.RestConnector)
	horizonConn.On("SendRequest", ctx, mock.Anything).Return(horizonRootResponse("777", "1")).Once()
	horizonBlock, err := test_utils.NewStellarHorizonChainSpecific(ctx, horizonConn).GetLatestBlock(ctx)
	require.NoError(t, err)

	assert.Equal(t, rpcBlock.Hash, horizonBlock.Hash)
	assert.Equal(t, rpcBlock.ParentHash, horizonBlock.ParentHash)
}

func TestStellarHorizonFinalizedBlockIsTheHead(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMockWithType(specs.RestConnector)
	conn.On("SendRequest", ctx, mock.Anything).Return(horizonRootResponse("100", "1")).Twice()

	specific := test_utils.NewStellarHorizonChainSpecific(ctx, conn)
	latest, err := specific.GetLatestBlock(ctx)
	require.NoError(t, err)
	finalized, err := specific.GetFinalizedBlock(ctx)
	require.NoError(t, err)

	assert.Equal(t, latest, finalized)
}

func TestStellarHorizonParseBlockRejectsZeroAndUnparseable(t *testing.T) {
	specific := test_utils.NewStellarHorizonChainSpecific(context.Background(), nil)

	_, err := specific.ParseBlock([]byte(`{"history_latest_ledger":0}`))
	assert.Error(t, err)

	_, err = specific.ParseBlock([]byte(`<html>`))
	assert.Error(t, err)
}

func TestStellarHorizonGetLatestBlockPropagatesUpstreamError(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMockWithType(specs.RestConnector)
	conn.On("SendRequest", ctx, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(503, "boom", nil)))

	_, err := test_utils.NewStellarHorizonChainSpecific(ctx, conn).GetLatestBlock(ctx)
	assert.Error(t, err)
}

func TestStellarHorizonProcessorsAndValidators(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMockWithType(specs.RestConnector)
	specific := test_utils.NewStellarHorizonChainSpecific(ctx, conn)

	assert.Nil(t, specific.CapDetectors(caps.DetectorInput{}))
	assert.Nil(t, specific.MethodsProcessor())
	assert.NotNil(t, specific.BlockProcessor())
	assert.NotNil(t, specific.LabelsProcessor())
	assert.NotNil(t, specific.LowerBoundProcessor())
	assert.Len(t, specific.SettingsValidators(), 1)
	assert.Empty(t, specific.HealthValidators())

	req, err := specific.SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "stellar: head subscriptions are not supported")
}
