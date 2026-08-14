package stellar_specific_test

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func healthResponse(latest, oldest string) protocol.ResponseHolder {
	body := `{"status":"healthy","latestLedger":` + latest + `,"oldestLedger":` + oldest +
		`,"latestLedgerCloseTime":"1784332881","ledgerRetentionWindow":120960}`
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.JsonRpc)
}

func TestStellarRpcSubscriptionsUnsupported(t *testing.T) {
	specific := test_utils.NewStellarRpcChainSpecific(context.Background(), nil)

	req, err := specific.SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.EqualError(t, err, "stellar: head subscriptions are not supported")

	block, err := specific.ParseSubscriptionBlock([]byte(`{}`))
	assert.Equal(t, protocol.ZeroBlock{}, block)
	assert.EqualError(t, err, "stellar: head subscriptions are not supported")
}

// getHealth is the head source: it is small, and it is the same document the
// bounds detector reads. getLatestLedger is never called internally.
func TestStellarRpcGetLatestBlockPollsGetHealth(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "getHealth"
	})).Return(healthResponse("63525714", "63404755")).Once()

	block, err := test_utils.NewStellarRpcChainSpecific(ctx, conn).GetLatestBlock(ctx)
	require.NoError(t, err)
	conn.AssertExpectations(t)

	hash, parentHash := specific_helpers.SyntheticHashes(63525714, 63525713)
	assert.Equal(t, uint64(63525714), block.Height)
	assert.Equal(t, hash, block.Hash)
	assert.Equal(t, parentHash, block.ParentHash)
}

// SCP closes ledgers final, so the finalized ledger is the head.
func TestStellarRpcFinalizedBlockIsTheHead(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", ctx, mock.Anything).Return(healthResponse("100", "1")).Twice()

	specific := test_utils.NewStellarRpcChainSpecific(ctx, conn)
	latest, err := specific.GetLatestBlock(ctx)
	require.NoError(t, err)
	finalized, err := specific.GetFinalizedBlock(ctx)
	require.NoError(t, err)

	assert.Equal(t, latest, finalized)
}

func TestStellarRpcHeadLinkageIsConsistentAcrossPolls(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", ctx, mock.Anything).Return(healthResponse("100", "1")).Once()
	conn.On("SendRequest", ctx, mock.Anything).Return(healthResponse("101", "1")).Once()

	specific := test_utils.NewStellarRpcChainSpecific(ctx, conn)
	first, err := specific.GetLatestBlock(ctx)
	require.NoError(t, err)
	second, err := specific.GetLatestBlock(ctx)
	require.NoError(t, err)

	assert.Equal(t, first.Hash, second.ParentHash)
}

func TestStellarRpcParseBlockRejectsZeroAndUnparseable(t *testing.T) {
	specific := test_utils.NewStellarRpcChainSpecific(context.Background(), nil)

	// sequence 0 would underflow the parent and cannot occur on a live network
	_, err := specific.ParseBlock([]byte(`{"status":"healthy","latestLedger":0}`))
	assert.Error(t, err)

	_, err = specific.ParseBlock([]byte(`not json`))
	assert.Error(t, err)
}

func TestStellarRpcGetLatestBlockPropagatesUpstreamError(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", ctx, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ResponseErrorWithData(-32603, "boom", nil)))

	_, err := test_utils.NewStellarRpcChainSpecific(ctx, conn).GetLatestBlock(ctx)
	assert.Error(t, err)
}

func TestStellarRpcProcessorsAndValidators(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	specific := test_utils.NewStellarRpcChainSpecific(ctx, conn)

	// no ws transport on stellar-rpc, so no ws-derived caps can be asserted
	assert.Nil(t, specific.CapDetectors(caps.DetectorInput{}))
	// neither API can be asked which methods it implements
	assert.Nil(t, specific.MethodsProcessor())

	assert.NotNil(t, specific.BlockProcessor())
	assert.NotNil(t, specific.LabelsProcessor())
	assert.NotNil(t, specific.LowerBoundProcessor())
	assert.Len(t, specific.SettingsValidators(), 1)
	// test options set validate-syncing=false, so the health validator is off
	assert.Empty(t, specific.HealthValidators())
}
