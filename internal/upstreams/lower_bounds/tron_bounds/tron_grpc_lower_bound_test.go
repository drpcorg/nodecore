package tron_bounds_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/tron_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	tronapi "github.com/drpcorg/public/pkg/tron/api"
	troncore "github.com/drpcorg/public/pkg/tron/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func tronGrpcBlock(t *testing.T, num int64) []byte {
	t.Helper()
	raw, err := proto.Marshal(&tronapi.BlockExtention{
		Blockid:     []byte{byte(num), 1, 2, 3},
		BlockHeader: &troncore.BlockHeader{RawData: &troncore.BlockHeaderRaw{Number: num, Timestamp: 1_700_000_000_000}},
	})
	require.NoError(t, err)
	return raw
}

func grpcOK(raw []byte) protocol.ResponseHolder {
	return protocol.NewGrpcUpstreamResponse("1", raw)
}

func grpcErr(code codes.Code) protocol.ResponseHolder {
	return protocol.NewGrpcUpstreamErrorResponse(
		protocol.NewInternalUpstreamGrpcRequest("/protocol.Wallet/GetBlock", nil, chains.TRON),
		&protocol.GrpcStatus{Code: code, Message: "boom"},
	)
}

func fastTronGrpc(d *tron_bounds.TronGrpcLowerBoundDetector) *tron_bounds.TronGrpcLowerBoundDetector {
	d.SetSearchRetryPolicy(3, time.Millisecond, time.Millisecond)
	return d
}

// tronGrpcBlockReq decodes a GetBlock body; ok is false for any other request.
func tronGrpcBlockReq(req protocol.RequestHolder) (*tronapi.BlockReq, bool) {
	if req.Method() != "/protocol.Wallet/GetBlock" || req.RequestType() != protocol.Grpc {
		return nil, false
	}
	body, err := req.Body()
	if err != nil {
		return nil, false
	}
	var msg tronapi.BlockReq
	if err := proto.Unmarshal(body, &msg); err != nil {
		return nil, false
	}
	return &msg, true
}

// tronGrpcProbeHeight extracts the requested height from a GetBlock probe
// (id_or_num set, detail false); the latest-block fetch has no id_or_num.
func tronGrpcProbeHeight(req protocol.RequestHolder) (int64, bool) {
	msg, ok := tronGrpcBlockReq(req)
	if !ok || msg.GetIdOrNum() == "" || msg.GetDetail() {
		return 0, false
	}
	height, err := strconv.ParseInt(msg.GetIdOrNum(), 10, 64)
	if err != nil {
		return 0, false
	}
	return height, true
}

func isTronGrpcNowBlock(req protocol.RequestHolder) bool {
	msg, ok := tronGrpcBlockReq(req)
	return ok && msg.GetIdOrNum() == "" && !msg.GetDetail()
}

func matchTronGrpcAtLeast(min int64) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		height, ok := tronGrpcProbeHeight(req)
		return ok && height >= min
	}
}

func matchTronGrpcBelow(limit int64) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		height, ok := tronGrpcProbeHeight(req)
		return ok && height < limit
	}
}

func TestTronGrpcLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	detector := tron_bounds.NewTronGrpcLowerBoundDetector("id", chains.TRON, time.Second, mocks.NewConnectorMockWithType(specs.GrpcConnector))

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{protocol.BlockBound, protocol.StateBound, protocol.TxBound, protocol.ReceiptsBound},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 3*time.Minute, detector.Period())
}

// Heights below the retention edge answer with an empty BlockExtention (no
// block id) - java-tron's "no such block" over gRPC. latest = 5, blocks >= 3
// present -> bound 3, fanned out to all four tron bound types.
func TestTronGrpcLowerBoundDetector_FansOutOneSearchToAllFourBoundTypes(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(isTronGrpcNowBlock)).Return(grpcOK(tronGrpcBlock(t, 5))).Once()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpcAtLeast(3))).Return(grpcOK(tronGrpcBlock(t, 3))).Maybe()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpcBelow(3))).Return(grpcOK(nil)).Maybe()

	detector := tron_bounds.NewTronGrpcLowerBoundDetector("id", chains.TRON, time.Second, connector)
	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, result, 4)
	got := make(map[protocol.LowerBoundType]int64, len(result))
	for _, b := range result {
		got[b.Type] = b.Bound
	}
	assert.Equal(t, int64(3), got[protocol.BlockBound])
	assert.Equal(t, int64(3), got[protocol.StateBound])
	assert.Equal(t, int64(3), got[protocol.TxBound])
	assert.Equal(t, int64(3), got[protocol.ReceiptsBound])
}

func TestTronGrpcLowerBoundDetector_AllAvailableReturnsOneForAllTypes(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(isTronGrpcNowBlock)).Return(grpcOK(tronGrpcBlock(t, 100))).Once()
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpcAtLeast(0))).Return(grpcOK(tronGrpcBlock(t, 1))).Maybe()

	detector := tron_bounds.NewTronGrpcLowerBoundDetector("id", chains.TRON, time.Second, connector)
	result, err := detector.DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, result, 4)
	for _, b := range result {
		assert.Equal(t, int64(1), b.Bound)
	}
}

// A gRPC status error on a probe is an outage, never "pruned": the detector
// must give up with an error instead of reporting a bound.
func TestTronGrpcLowerBoundProbeErrorIsNotPruned(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(isTronGrpcNowBlock)).Return(grpcOK(tronGrpcBlock(t, 100)))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(matchTronGrpcAtLeast(0))).Return(grpcErr(codes.Unavailable))

	detector := fastTronGrpc(tron_bounds.NewTronGrpcLowerBoundDetector("id", chains.TRON, time.Second, connector))
	bounds, err := detector.DetectLowerBound(context.Background())

	assert.Error(t, err)
	assert.Empty(t, bounds)
}

func TestTronGrpcLowerBoundDetector_LatestErrorFails(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(isTronGrpcNowBlock)).Return(grpcErr(codes.Unavailable))

	detector := fastTronGrpc(tron_bounds.NewTronGrpcLowerBoundDetector("id", chains.TRON, time.Second, connector))
	_, err := detector.DetectLowerBound(context.Background())

	assert.ErrorContains(t, err, "cannot fetch latest height")
}

func TestTronGrpcLowerBoundDetector_LatestEmptyBodyFails(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(isTronGrpcNowBlock)).Return(grpcOK(nil))

	detector := fastTronGrpc(tron_bounds.NewTronGrpcLowerBoundDetector("id", chains.TRON, time.Second, connector))
	_, err := detector.DetectLowerBound(context.Background())

	require.Error(t, err)
}
