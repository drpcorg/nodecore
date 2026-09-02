package cosmos_specific_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	tendermintv1beta1 "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	"cosmossdk.io/api/tendermint/types"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/cosmos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/cosmos_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func freshCosmosGrpc(t *testing.T, connector *mocks.ConnectorMock, opts *chains.Options) *cosmos_specific.CosmosGrpcSpecific {
	t.Helper()
	if opts == nil {
		opts = cosmosOptions(false)
	}
	cs, err := cosmos_specific.NewCosmosSpecific(
		context.Background(),
		"upstream-id",
		connector,
		chains.GetChain("cosmos-hub"),
		100*time.Millisecond,
		opts,
	)
	require.NoError(t, err)
	grpcSpecific, ok := cs.(*cosmos_specific.CosmosGrpcSpecific)
	require.True(t, ok)
	return grpcSpecific
}

// ---------- dispatch ----------

func TestNewCosmosSpecificDispatchesGrpc(t *testing.T) {
	cs := freshCosmosGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)
	assert.NotNil(t, cs)
}

func TestNewCosmosGrpcSpecificRejectsOtherConnectors(t *testing.T) {
	cs, err := cosmos_specific.NewCosmosGrpcSpecific(
		context.Background(), "id",
		mocks.NewConnectorMockWithType(specs.RestConnector),
		chains.GetChain("cosmos-hub"), time.Second, cosmosOptions(false),
	)
	assert.Nil(t, cs)
	assert.ErrorContains(t, err, "cosmos grpc specific supports only the grpc connector")
}

// ---------- head / blocks ----------

// grpcHash builds a deterministic 32-byte hash out of a seed, the way the
// gRPC API reports block ids - raw bytes.
func grpcHash(seed byte) []byte {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	return raw
}

func cosmosGrpcBlockBytes(t *testing.T, height int64, hash, parentHash []byte) []byte {
	t.Helper()
	data, err := proto.Marshal(&tendermintv1beta1.GetLatestBlockResponse{
		BlockId: &types.BlockID{Hash: hash},
		SdkBlock: &tendermintv1beta1.Block{
			Header: &tendermintv1beta1.Header{
				Height:      height,
				LastBlockId: &types.BlockID{Hash: parentHash},
			},
		},
	})
	require.NoError(t, err)
	return data
}

// cosmosGrpcLegacyBlockBytes renders the reply of a pre-0.47 SDK node: only
// the deprecated comet `block` field is populated, `sdk_block` is absent.
func cosmosGrpcLegacyBlockBytes(t *testing.T, height int64, hash, parentHash []byte) []byte {
	t.Helper()
	data, err := proto.Marshal(&tendermintv1beta1.GetLatestBlockResponse{
		BlockId: &types.BlockID{Hash: hash},
		Block: &types.Block{ //nolint:staticcheck // deliberately exercises the deprecated field old nodes still send
			Header: &types.Header{
				Height:      height,
				LastBlockId: &types.BlockID{Hash: parentHash},
			},
		},
	})
	require.NoError(t, err)
	return data
}

func matchCosmosGrpc(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method && req.RequestType() == protocol.Grpc
	}
}

func TestCosmosGrpcGetLatestBlock(t *testing.T) {
	hash, parentHash := grpcHash(1), grpcHash(2)
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetLatestBlock"))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcBlockBytes(t, 25000000, hash, parentHash))).
		Once()

	cs := freshCosmosGrpc(t, connector, nil)
	block, err := cs.GetLatestBlock(context.Background())

	require.NoError(t, err)
	assert.Equal(t, uint64(25000000), block.Height)
	assert.Equal(t, blockchain.NewHashIdFromBytes(hash), block.Hash)
	assert.Equal(t, blockchain.NewHashIdFromBytes(parentHash), block.ParentHash)
	connector.AssertExpectations(t)
}

// The gRPC API reports the same block hash the LCD does, only as raw bytes
// instead of base64. Both must reduce to the same HashId, otherwise two
// upstreams of one chain would appear to disagree about the head.
func TestCosmosGrpcHashEncodingAgreesWithRest(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i * 7)
	}

	fromGrpc, err := freshCosmosGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil).
		ParseBlock(cosmosGrpcBlockBytes(t, 100, raw, raw))
	require.NoError(t, err)
	fromLcd, err := freshCosmosRest(t, mocks.NewConnectorMockWithType(specs.RestConnector), nil).
		ParseBlock([]byte(cosmosBlockJSON(100, base64.StdEncoding.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw))))
	require.NoError(t, err)

	assert.Equal(t, blockchain.NewHashIdFromBytes(raw), fromGrpc.Hash)
	assert.Equal(t, fromLcd.Hash, fromGrpc.Hash)
	assert.Equal(t, fromLcd.ParentHash, fromGrpc.ParentHash)
	assert.Equal(t, fromLcd.Height, fromGrpc.Height)
}

func TestCosmosGrpcGetFinalizedBlockIsTheHead(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchCosmosGrpc("/cosmos.base.tendermint.v1beta1.Service/GetLatestBlock"))).
		Return(protocol.NewGrpcUpstreamResponse("1", cosmosGrpcBlockBytes(t, 7, grpcHash(3), grpcHash(4)))).
		Twice()

	cs := freshCosmosGrpc(t, connector, nil)
	latest, err := cs.GetLatestBlock(context.Background())
	require.NoError(t, err)
	finalized, err := cs.GetFinalizedBlock(context.Background())
	require.NoError(t, err)

	assert.Equal(t, latest, finalized)
	connector.AssertExpectations(t)
}

// A pre-0.47 SDK node fills only the deprecated comet `block` field; the
// parser must fall back to it when `sdk_block` is absent.
func TestCosmosGrpcParseBlockFallsBackToCometBlock(t *testing.T) {
	hash, parentHash := grpcHash(9), grpcHash(10)
	cs := freshCosmosGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	block, err := cs.ParseBlock(cosmosGrpcLegacyBlockBytes(t, 42, hash, parentHash))

	require.NoError(t, err)
	assert.Equal(t, uint64(42), block.Height)
	assert.Equal(t, blockchain.NewHashIdFromBytes(hash), block.Hash)
	assert.Equal(t, blockchain.NewHashIdFromBytes(parentHash), block.ParentHash)
}

func TestCosmosGrpcParseBlockRejectsGarbage(t *testing.T) {
	cs := freshCosmosGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	for name, payload := range map[string][]byte{
		"invalid proto": {0xff, 0xff, 0xff},
		"empty message": {},
		"zero height":   cosmosGrpcBlockBytes(t, 0, grpcHash(5), grpcHash(6)),
	} {
		block, err := cs.ParseBlock(payload)
		assert.True(t, block.IsFullEmpty(), name)
		assert.Error(t, err, name)
	}
}

func TestCosmosGrpcHeadSubscriptionsUnsupported(t *testing.T) {
	cs := freshCosmosGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	req, err := cs.SubscribeHeadRequest()
	assert.Nil(t, req)
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)

	block, err := cs.ParseSubscriptionBlock([]byte{})
	assert.True(t, block.IsFullEmpty())
	assert.ErrorIs(t, err, blocks.ErrUnsupportedHeadSubscriptions)
}

// ---------- wiring ----------

// The gRPC API exposes no peer set, so there is only ever the syncing validator.
func TestCosmosGrpcHealthValidators(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)

	assert.Empty(t, freshCosmosGrpc(t, connector, cosmosOptions(false)).HealthValidators())

	enabled := freshCosmosGrpc(t, connector, cosmosOptions(true)).HealthValidators()
	require.Len(t, enabled, 1)
	assert.IsType(t, &cosmos_validations.CosmosGrpcSyncingValidator{}, enabled[0])
}

func TestCosmosGrpcSettingsValidators(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)

	validators := freshCosmosGrpc(t, connector, nil).SettingsValidators()
	require.Len(t, validators, 1)
	assert.IsType(t, &cosmos_validations.CosmosGrpcChainValidator{}, validators[0])

	disabled := cosmosOptions(false)
	disabled.DisableChainValidation = new(true)
	assert.Empty(t, freshCosmosGrpc(t, connector, disabled).SettingsValidators())
}

func TestCosmosGrpcProcessorsArePresent(t *testing.T) {
	cs := freshCosmosGrpc(t, mocks.NewConnectorMockWithType(specs.GrpcConnector), nil)

	assert.NotNil(t, cs.LowerBoundProcessor())
	assert.NotNil(t, cs.LabelsProcessor())
	assert.NotNil(t, cs.BlockProcessor())
}
