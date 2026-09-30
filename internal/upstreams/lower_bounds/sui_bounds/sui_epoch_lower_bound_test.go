package sui_bounds_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/sui_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/drpcorg/public/pkg/sui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

const (
	suiServiceInfoMethod = "/sui.rpc.v2.LedgerService/GetServiceInfo"
	suiGetEpochMethod    = "/sui.rpc.v2.LedgerService/GetEpoch"
)

func suiChain() chains.Chain {
	return chains.GetChain("sui").Chain
}

func matchSuiGrpc(method string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		return req.Method() == method && req.RequestType() == protocol.Grpc
	}
}

func serviceInfoBytes(t *testing.T, epoch uint64) []byte {
	t.Helper()
	data, err := proto.Marshal(&sui.GetServiceInfoResponse{
		Epoch:            new(epoch),
		CheckpointHeight: new(uint64(328626601)),
	})
	require.NoError(t, err)
	return data
}

func epochBytes(t *testing.T, epoch uint64) []byte {
	t.Helper()
	data, err := proto.Marshal(&sui.GetEpochResponse{Epoch: &sui.Epoch{Epoch: new(epoch)}})
	require.NoError(t, err)
	return data
}

// probedEpochMatches matches a GetEpoch probe whose epoch satisfies the
// predicate. The probe must ask only for the epoch number - a heavier read
// mask (committee, system state) turns a cheap existence check into a bulky
// response - so a probe with any other mask matches nothing and fails the
// test. Matchers never assert, only report.
func probedEpochMatches(predicate func(uint64) bool) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != suiGetEpochMethod || req.RequestType() != protocol.Grpc {
			return false
		}
		body, err := req.Body()
		if err != nil {
			return false
		}
		var probe sui.GetEpochRequest
		if err := proto.Unmarshal(body, &probe); err != nil {
			return false
		}
		if probe.Epoch == nil || len(probe.GetReadMask().GetPaths()) != 1 || probe.GetReadMask().GetPaths()[0] != "epoch" {
			return false
		}
		return predicate(probe.GetEpoch())
	}
}

func epochGrpcError(code codes.Code, message string) protocol.ResponseHolder {
	return protocol.NewGrpcUpstreamErrorResponse(
		protocol.NewInternalUpstreamGrpcRequest(suiGetEpochMethod, nil, suiChain()),
		&protocol.GrpcStatus{Code: code, Message: message},
	)
}

func newEpochDetector(connector *mocks.ConnectorMock) *sui_bounds.SuiEpochLowerBoundDetector {
	detector := sui_bounds.NewSuiEpochLowerBoundDetector("id", suiChain(), time.Second, connector)
	detector.SetSearchRetryPolicy(1, time.Millisecond, time.Millisecond)
	return detector
}

// The steady-state path: the rpc-store epochs index starts wherever it was
// seeded, so epochs below it come back as NOT_FOUND ("Epoch N not found")
// and everything from the seed epoch up to the current one is served.
func TestSuiEpochLowerBoundSearchFindsFirstServedEpoch(t *testing.T) {
	const firstServed = 1210
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSuiGrpc(suiServiceInfoMethod))).
		Return(protocol.NewGrpcUpstreamResponse("1", serviceInfoBytes(t, 1266)))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(probedEpochMatches(func(e uint64) bool { return e < firstServed }))).
		Return(epochGrpcError(codes.NotFound, "Epoch 1 not found"))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(probedEpochMatches(func(e uint64) bool { return e >= firstServed }))).
		Return(protocol.NewGrpcUpstreamResponse("1", epochBytes(t, firstServed)))

	bounds, err := newEpochDetector(connector).DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, bounds, 1)
	assert.Equal(t, int64(firstServed), bounds[0].Bound)
	assert.Equal(t, protocol.EpochBound, bounds[0].Type)
}

// Only NOT_FOUND means "not served". A transient status must surface as an
// error so the previously published bound stays instead of an outage being
// read as pruning.
func TestSuiEpochLowerBoundTransientErrorIsNotPruned(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSuiGrpc(suiServiceInfoMethod))).
		Return(protocol.NewGrpcUpstreamResponse("1", serviceInfoBytes(t, 1266)))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSuiGrpc(suiGetEpochMethod))).
		Return(epochGrpcError(codes.Unavailable, "node is overloaded"))

	bounds, err := newEpochDetector(connector).DetectLowerBound(context.Background())

	require.Error(t, err)
	assert.Empty(t, bounds)
}

// A response that carries no epoch payload is not a served epoch either -
// the probe must look at the response, not just at the absence of an error.
func TestSuiEpochLowerBoundEmptyResponseIsAnError(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSuiGrpc(suiServiceInfoMethod))).
		Return(protocol.NewGrpcUpstreamResponse("1", serviceInfoBytes(t, 1266)))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSuiGrpc(suiGetEpochMethod))).
		Return(protocol.NewGrpcUpstreamResponse("1", []byte{}))

	bounds, err := newEpochDetector(connector).DetectLowerBound(context.Background())

	require.Error(t, err)
	assert.Empty(t, bounds)
}

// A node whose index reaches back to genesis serves epoch 0. The shared
// search coerces a genesis floor to 1, as it does for every other chain, so
// the published bound is 1 rather than 0.
func TestSuiEpochLowerBoundArchiveNodeReportsOne(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSuiGrpc(suiServiceInfoMethod))).
		Return(protocol.NewGrpcUpstreamResponse("1", serviceInfoBytes(t, 8)))
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(probedEpochMatches(func(uint64) bool { return true }))).
		Return(protocol.NewGrpcUpstreamResponse("1", epochBytes(t, 0)))

	bounds, err := newEpochDetector(connector).DetectLowerBound(context.Background())

	require.NoError(t, err)
	require.Len(t, bounds, 1)
	assert.Equal(t, int64(1), bounds[0].Bound)
	assert.Equal(t, protocol.EpochBound, bounds[0].Type)
}

// A service info without an epoch gives the search no upper end.
func TestSuiEpochLowerBoundNoLatestEpochIsAnError(t *testing.T) {
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchSuiGrpc(suiServiceInfoMethod))).
		Return(protocol.NewGrpcUpstreamResponse("1", []byte{}))

	bounds, err := newEpochDetector(connector).DetectLowerBound(context.Background())

	require.Error(t, err)
	assert.Empty(t, bounds)
}

func TestSuiEpochLowerBoundDetectorShape(t *testing.T) {
	detector := sui_bounds.NewSuiEpochLowerBoundDetector("id", suiChain(), time.Second, mocks.NewConnectorMock())

	assert.Equal(t, []protocol.LowerBoundType{protocol.EpochBound}, detector.SupportedTypes())
	assert.Equal(t, time.Hour, detector.Period())
}
