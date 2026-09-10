package sui_specific_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/sui_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/public/pkg/sui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// suiMainnetChainId is the genesis checkpoint digest chains.yaml declares for
// SUI_MAINNET (the registry lowercases chain-ids on load).
const suiMainnetChainId = "4btiuiMPvEENsttpZC7CZ53DruC3MAgfznDbASZ7DR6S"

func serviceInfoBytes(t *testing.T, serviceInfo *sui.GetServiceInfoResponse) []byte {
	t.Helper()
	data, err := proto.Marshal(serviceInfo)
	require.NoError(t, err)
	return data
}

func serviceInfoResponse(t *testing.T, serviceInfo *sui.GetServiceInfoResponse) protocol.ResponseHolder {
	return protocol.NewGrpcUpstreamResponse("1", serviceInfoBytes(t, serviceInfo))
}

func fullServiceInfo() *sui.GetServiceInfoResponse {
	return &sui.GetServiceInfoResponse{
		ChainId:                          new(suiMainnetChainId),
		Chain:                            new("mainnet"),
		Epoch:                            new(uint64(500)),
		CheckpointHeight:                 new(uint64(120000000)),
		LowestAvailableCheckpoint:        new(uint64(1000)),
		LowestAvailableCheckpointObjects: new(uint64(50000)),
		Server:                           new("sui-node/1.78.0-03113679fb97"),
	}
}

func TestSuiSubscribeHeadRequest(t *testing.T) {
	specific := test_utils.NewSuiChainSpecific(context.Background(), nil)

	req, err := specific.SubscribeHeadRequest()
	require.NoError(t, err)
	assert.Equal(t, "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints", req.Method())
	assert.Equal(t, protocol.Grpc, req.RequestType())

	body, err := req.Body()
	require.NoError(t, err)
	var request sui.SubscribeCheckpointsRequest
	require.NoError(t, proto.Unmarshal(body, &request))
	assert.Equal(t, []string{"sequence_number"}, request.GetReadMask().GetPaths())
	assert.Nil(t, request.Filter, "the head stream is unfiltered: every frame carries a checkpoint")
}

func TestSuiParseSubscriptionBlockUsesTheCursor(t *testing.T) {
	specific := test_utils.NewSuiChainSpecific(context.Background(), nil)
	frame, err := proto.Marshal(&sui.SubscribeCheckpointsResponse{
		Cursor:     new(uint64(120000000)),
		Checkpoint: &sui.Checkpoint{SequenceNumber: new(uint64(120000000))},
	})
	require.NoError(t, err)

	block, err := specific.ParseSubscriptionBlock(frame)
	require.NoError(t, err)

	hash, parentHash := specific_helpers.SyntheticHashes(120000000, 119999999)
	assert.Equal(t, uint64(120000000), block.Height)
	assert.Equal(t, hash, block.Hash)
	assert.Equal(t, parentHash, block.ParentHash)
	assert.Equal(t, frame, block.RawData)
}

func TestSuiParseSubscriptionBlockWithoutCursorIsAnError(t *testing.T) {
	specific := test_utils.NewSuiChainSpecific(context.Background(), nil)
	frame, err := proto.Marshal(&sui.SubscribeCheckpointsResponse{})
	require.NoError(t, err)

	block, err := specific.ParseSubscriptionBlock(frame)
	assert.Equal(t, protocol.ZeroBlock{}, block)
	assert.EqualError(t, err, "sui SubscribeCheckpoints frame carries no cursor")
}

func TestSuiParseSubscriptionBlockGarbageIsAnError(t *testing.T) {
	specific := test_utils.NewSuiChainSpecific(context.Background(), nil)

	_, err := specific.ParseSubscriptionBlock([]byte{0xff, 0xff, 0xff})
	assert.ErrorContains(t, err, "couldn't parse the sui SubscribeCheckpoints frame")
}

func TestSuiGetLatestBlockPollsGetServiceInfo(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	rawData := serviceInfoBytes(t, fullServiceInfo())
	conn.On("SendRequest", ctx, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "/sui.rpc.v2.LedgerService/GetServiceInfo" && r.RequestType() == protocol.Grpc
	})).Return(protocol.NewGrpcUpstreamResponse("1", rawData)).Once()

	block, err := test_utils.NewSuiChainSpecific(ctx, conn).GetLatestBlock(ctx)
	require.NoError(t, err)
	conn.AssertExpectations(t)

	hash, parentHash := specific_helpers.SyntheticHashes(120000000, 119999999)
	assert.Equal(t, uint64(120000000), block.Height)
	assert.Equal(t, hash, block.Hash)
	assert.Equal(t, parentHash, block.ParentHash)
	assert.Equal(t, rawData, block.RawData)
}

// an executed checkpoint is final, so the finalized block is the head
func TestSuiFinalizedBlockIsTheHead(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", ctx, mock.Anything).Return(serviceInfoResponse(t, fullServiceInfo())).Twice()

	specific := test_utils.NewSuiChainSpecific(ctx, conn)
	latest, err := specific.GetLatestBlock(ctx)
	require.NoError(t, err)
	finalized, err := specific.GetFinalizedBlock(ctx)
	require.NoError(t, err)

	assert.Equal(t, latest.Height, finalized.Height)
}

func TestSuiParseBlock(t *testing.T) {
	specific := test_utils.NewSuiChainSpecific(context.Background(), nil)

	block, err := specific.ParseBlock(serviceInfoBytes(t, fullServiceInfo()))
	require.NoError(t, err)
	assert.Equal(t, uint64(120000000), block.Height)

	_, err = specific.ParseBlock([]byte("not a protobuf"))
	assert.ErrorContains(t, err, "couldn't parse the sui GetServiceInfo result")
}

func TestSuiBlockWithoutCheckpointHeightIsAnError(t *testing.T) {
	specific := test_utils.NewSuiChainSpecific(context.Background(), nil)

	_, err := specific.ParseBlock(serviceInfoBytes(t, &sui.GetServiceInfoResponse{Chain: new("mainnet")}))
	assert.EqualError(t, err, "sui node reported no checkpoint_height")
}

func TestSuiCapDetectorsAndMethodsProcessorAreEmpty(t *testing.T) {
	specific := test_utils.NewSuiChainSpecific(context.Background(), nil)

	assert.Nil(t, specific.CapDetectors(caps.DetectorInput{}))
	assert.Nil(t, specific.MethodsProcessor())
}

func TestSuiHealthValidator(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.Anything).Return(serviceInfoResponse(t, fullServiceInfo())).Once()

	validators := test_utils.NewSuiChainSpecific(ctx, conn).HealthValidators()
	require.Len(t, validators, 1)
	assert.Equal(t, protocol.Available, validators[0].Validate())

	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewTotalFailure(protocol.NewUpstreamGrpcRequest("1", "/pkg.S/M", nil, nil, ""), protocol.ServerError())).Once()
	assert.Equal(t, protocol.Unavailable, validators[0].Validate())
}

func TestSuiChainValidator(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()

	validators := test_utils.NewSuiChainSpecific(ctx, conn).SettingsValidators()
	require.Len(t, validators, 1)

	// the digest matches case-insensitively - the registry lowercases chain-ids
	conn.On("SendRequest", mock.Anything, mock.Anything).Return(serviceInfoResponse(t, fullServiceInfo())).Once()
	assert.Equal(t, validations.Valid, validators[0].Validate())

	wrongChain := fullServiceInfo()
	wrongChain.ChainId = new("69WiPg3DAQiwdxfncX6wYQ2siKwAe6L9BZthQea3JNMD")
	conn.On("SendRequest", mock.Anything, mock.Anything).Return(serviceInfoResponse(t, wrongChain)).Once()
	assert.Equal(t, validations.FatalSettingError, validators[0].Validate())
}

func TestSuiLowerBoundDetector(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.Anything).Return(serviceInfoResponse(t, fullServiceInfo())).Once()

	detector := sui_bounds.NewSuiLowerBoundDetector("id", chains.GetChain("sui").Chain, time.Second, conn)
	bounds, err := detector.DetectLowerBound(ctx)
	require.NoError(t, err)

	require.Len(t, bounds, 2)
	assert.Equal(t, int64(1000), bounds[0].Bound)
	assert.Equal(t, protocol.BlockBound, bounds[0].Type)
	assert.Equal(t, int64(50000), bounds[1].Bound)
	assert.Equal(t, protocol.StateBound, bounds[1].Type)
}

func TestSuiLowerBoundDetectorSkipsAbsentBounds(t *testing.T) {
	ctx := context.Background()
	conn := mocks.NewConnectorMock()
	serviceInfo := fullServiceInfo()
	serviceInfo.LowestAvailableCheckpoint = nil
	conn.On("SendRequest", mock.Anything, mock.Anything).Return(serviceInfoResponse(t, serviceInfo)).Once()

	bounds, err := sui_bounds.NewSuiLowerBoundDetector("id", chains.GetChain("sui").Chain, time.Second, conn).DetectLowerBound(ctx)
	require.NoError(t, err)

	require.Len(t, bounds, 1)
	assert.Equal(t, protocol.StateBound, bounds[0].Type)
}
