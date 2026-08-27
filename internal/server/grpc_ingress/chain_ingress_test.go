package grpc_ingress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/stats"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/drpcorg/public/pkg/sui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	v1reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

const suiGetServiceInfo = "/sui.rpc.v2.LedgerService/GetServiceInfo"

func startIngressServer(t testing.TB, appCtx *server_ctx.ApplicationServerContext, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := NewServer(appCtx)
	if register != nil {
		register(server)
	}
	go func() {
		_ = server.Serve(listener)
	}()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
	})
	return conn
}

// ingressAppCtx builds an application context whose sui chain has one live
// upstream served by the given connector mock.
func ingressAppCtx(t *testing.T, connector *mocks.ConnectorMock) *server_ctx.ApplicationServerContext {
	t.Helper()
	authProc := mocks.NewMockAuthProcessor()
	authProc.On("Authenticate", mock.Anything, mock.Anything).Return(nil)
	authProc.On("PreKeyValidate", mock.Anything, mock.Anything).Return(nil, nil)
	authProc.On("PostKeyValidate", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	authProc.On("GetKeyValue", mock.Anything).Return("")
	return ingressAppCtxWithAuth(t, connector, authProc)
}

// ingressAppCtxWithAuth is ingressAppCtx with a caller-provided auth processor.
func ingressAppCtxWithAuth(t *testing.T, connector *mocks.ConnectorMock, authProc auth.AuthProcessor) *server_ctx.ApplicationServerContext {
	t.Helper()
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string](suiGetServiceInfo, suiListCheckpoints, suiSubscribeCheckpoints))
	methodsMock.On("HasMethod", mock.Anything).Return(true)

	upstream := test_utils.TestEvmUpstream(connector, &config.Upstream{
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
		Options:      &chains.Options{InternalTimeout: 5 * time.Second},
	}, methodsMock, nil)

	chainSupervisor := upstreams.NewGenericChainSupervisor(
		t.Context(), chains.SUI, fork_choice.NewHeightForkChoice(), dimensions.NewGenericDimensionTracker(), false, nil,
	)
	go chainSupervisor.Start()
	state := protocol.DefaultUpstreamState(methodsMock, mapset.NewThreadUnsafeSet[protocol.Cap](), "00012", nil, nil)
	state.HeadData = protocol.Block{Height: 100}
	chainSupervisor.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        "id",
		Chain:     chains.SUI,
		EventType: &protocol.StateUpstreamEvent{State: &state},
	})
	time.Sleep(20 * time.Millisecond)

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.SUI).Return(chainSupervisor)
	upSup.On("GetExecutor").Return(test_utils.CreateExecutor())
	upSup.On("GetUpstream", "id").Return(upstream)

	cacheProcessor := mocks.NewCacheProcessorMock()
	cacheProcessor.On("Receive", mock.Anything, mock.Anything, mock.Anything).Return([]byte(nil), false)
	cacheProcessor.On("Store", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

	appConfig := &config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
			BalancingStrategy: config.BaseBalancingStrategy,
			IntegrityConfig:   &config.IntegrityConfig{},
		},
	}

	return server_ctx.NewApplicationServerContext(
		upSup,
		cacheProcessor,
		nil,
		authProc,
		appConfig,
		nil,
		stats.NewStatsService(t.Context(), nil, nil),
		dimensions.NewGenericDimensionTracker(),
		nil,
		subengine.NewRegistry(t.Context()),
	)
}

func suiIngressContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "sui", "x-nodecore-key", "some-key")
}

// A typed client calls a chain method on the catch-all: the request bytes
// reach the upstream verbatim, the response decodes with the client's own
// generated stubs, and the upstream's filtered metadata arrives in the right
// buckets (headers as headers, trailers as trailers).
func TestChainIngressUnaryCall(t *testing.T) {
	serviceInfo := &sui.GetServiceInfoResponse{
		Chain:            new("mainnet"),
		CheckpointHeight: new(uint64(42)),
	}
	respBytes, err := proto.Marshal(serviceInfo)
	require.NoError(t, err)

	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		body, bodyErr := r.Body()
		return r.Method() == suiGetServiceInfo && r.RequestType() == protocol.Grpc && bodyErr == nil && len(body) == 0
	})).Return(
		protocol.NewGrpcUpstreamResponse("1", respBytes).
			WithResponseHeaders(http.Header{"x-up-meta": {"checkpoint-42"}}).
			WithResponseTrailers(map[string][]string{"x-up-trailer": {"tv"}}),
	).Once()

	conn := startIngressServer(t, ingressAppCtx(t, connector), nil)

	var reply sui.GetServiceInfoResponse
	var header, trailer metadata.MD
	err = conn.Invoke(suiIngressContext(t), suiGetServiceInfo,
		&sui.GetServiceInfoRequest{}, &reply, grpc.Header(&header), grpc.Trailer(&trailer))
	require.NoError(t, err)
	connector.AssertExpectations(t)

	assert.Equal(t, "mainnet", reply.GetChain())
	assert.Equal(t, uint64(42), reply.GetCheckpointHeight())
	assert.Equal(t, []string{"checkpoint-42"}, header.Get("x-up-meta"))
	assert.Equal(t, []string{"tv"}, trailer.Get("x-up-trailer"))
}

// The upstream's verbatim status - typed details included - reaches the client.
func TestChainIngressUpstreamStatusRidesVerbatim(t *testing.T) {
	upstreamStatus, err := status.New(codes.NotFound, "object not found").
		WithDetails(&errdetails.ErrorInfo{Reason: "OBJECT_PRUNED", Domain: "sui.io"})
	require.NoError(t, err)
	statusProto, err := proto.Marshal(upstreamStatus.Proto())
	require.NoError(t, err)

	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	dummyRequest := protocol.NewUpstreamGrpcRequest("1", suiGetServiceInfo, nil, nil, "")
	connector.On("SendRequest", mock.Anything, mock.Anything).Return(
		protocol.NewGrpcUpstreamErrorResponse(dummyRequest, &protocol.GrpcStatus{
			Code:        codes.NotFound,
			Message:     "object not found",
			StatusProto: statusProto,
		}),
	).Once()

	conn := startIngressServer(t, ingressAppCtx(t, connector), nil)

	var reply sui.GetServiceInfoResponse
	err = conn.Invoke(suiIngressContext(t), suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &reply)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Equal(t, "object not found", st.Message())
	require.Len(t, st.Details(), 1)
	errorInfo, ok := st.Details()[0].(*errdetails.ErrorInfo)
	require.True(t, ok)
	assert.Equal(t, "OBJECT_PRUNED", errorInfo.Reason)
}

func TestChainIngressUnknownMethod(t *testing.T) {
	conn := startIngressServer(t, ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector)), nil)

	err := conn.Invoke(suiIngressContext(t), "/sui.rpc.v2.LedgerService/Bogus",
		&sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})

	assert.Equal(t, codes.Unimplemented, status.Code(err))
	assert.ErrorContains(t, err, "unknown method /sui.rpc.v2.LedgerService/Bogus")
}

const suiListCheckpoints = "/sui.rpc.v2.LedgerService/ListCheckpoints"
const suiSubscribeCheckpoints = "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints"

var serverStreamDesc = &grpc.StreamDesc{StreamName: "stream", ServerStreams: true}

// openStream issues a server-streaming call the way a generated client would.
func openStream(t *testing.T, conn *grpc.ClientConn, ctx context.Context, method string, request proto.Message) grpc.ClientStream {
	t.Helper()
	stream, err := conn.NewStream(ctx, serverStreamDesc, method)
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(request))
	require.NoError(t, stream.CloseSend())
	return stream
}

func checkpointFrame(t *testing.T, height uint64) []byte {
	t.Helper()
	data, err := proto.Marshal(&sui.ListCheckpointsResponse{Checkpoint: &sui.Checkpoint{SequenceNumber: new(height)}})
	require.NoError(t, err)
	return data
}

// streamingConnector returns a grpc connector mock whose Subscribe hands out
// frames and, when subscribeCtx is non-nil, publishes the ctx it was called with
// (buffer the channel with 1).
func streamingConnector(t *testing.T, frames chan protocol.SubResponse, subscribeCtx chan context.Context) *mocks.ConnectorMock {
	t.Helper()
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("Subscribe", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		if subscribeCtx != nil {
			subscribeCtx <- args.Get(0).(context.Context)
		}
	}).Return(protocol.NewGrpcUpstreamSubscriptionResponse(frames, "op-1"), nil)
	connector.On("SubscribeStates", mock.Anything).Return(nil)
	connector.On("Unsubscribe", mock.Anything).Return().Maybe()
	return connector
}

func TestChainIngressFiniteStreamCompletesWithOkAndTrailers(t *testing.T) {
	frames := make(chan protocol.SubResponse)
	connector := streamingConnector(t, frames, nil)
	conn := startIngressServer(t, ingressAppCtx(t, connector), nil)
	go func() {
		frames <- &protocol.GrpcSubResponse{Message: checkpointFrame(t, 1), UpstreamId: "id", Headers: http.Header{"x-up-meta": {"h"}}}
		frames <- &protocol.GrpcSubResponse{Message: checkpointFrame(t, 2), UpstreamId: "id"}
		frames <- &protocol.GrpcSubResponse{End: true, UpstreamId: "id", Trailers: map[string][]string{"x-up-trailer": {"t"}}}
		close(frames)
	}()

	stream := openStream(t, conn, suiIngressContext(t), suiListCheckpoints, &sui.ListCheckpointsRequest{})

	var heights []uint64
	for {
		var reply sui.ListCheckpointsResponse
		err := stream.RecvMsg(&reply)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		heights = append(heights, reply.GetCheckpoint().GetSequenceNumber())
	}
	assert.Equal(t, []uint64{1, 2}, heights)
	header, err := stream.Header()
	require.NoError(t, err)
	assert.Equal(t, []string{"h"}, header.Get("x-up-meta"))
	assert.Equal(t, []string{"t"}, stream.Trailer().Get("x-up-trailer"))
	connector.AssertCalled(t, "Subscribe", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == suiListCheckpoints && r.IsSubscribe()
	}))
}

func TestChainIngressSubscriptionClosedByNodeIsUnavailable(t *testing.T) {
	frames := make(chan protocol.SubResponse)
	conn := startIngressServer(t, ingressAppCtx(t, streamingConnector(t, frames, nil)), nil)
	go func() {
		frames <- &protocol.GrpcSubResponse{Message: checkpointFrame(t, 1), UpstreamId: "id"}
		frames <- &protocol.GrpcSubResponse{End: true, UpstreamId: "id"}
		close(frames)
	}()

	stream := openStream(t, conn, suiIngressContext(t), suiSubscribeCheckpoints, &sui.SubscribeCheckpointsRequest{})

	require.NoError(t, stream.RecvMsg(&sui.SubscribeCheckpointsResponse{}))
	err := stream.RecvMsg(&sui.SubscribeCheckpointsResponse{})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

func TestChainIngressStreamUpstreamStatusRidesVerbatim(t *testing.T) {
	frames := make(chan protocol.SubResponse)
	conn := startIngressServer(t, ingressAppCtx(t, streamingConnector(t, frames, nil)), nil)
	go func() {
		frames <- &protocol.GrpcSubResponse{
			Error:      protocol.NewGrpcStatusResponseError(&protocol.GrpcStatus{Code: codes.ResourceExhausted, Message: "slow down"}),
			UpstreamId: "id",
			Trailers:   map[string][]string{"x-rate-limit": {"0"}},
		}
		close(frames)
	}()

	stream := openStream(t, conn, suiIngressContext(t), suiListCheckpoints, &sui.ListCheckpointsRequest{})

	err := stream.RecvMsg(&sui.ListCheckpointsResponse{})
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.ErrorContains(t, err, "slow down")
	assert.Equal(t, []string{"0"}, stream.Trailer().Get("x-rate-limit"))
}

func TestChainIngressClientCancelStopsTheUpstreamStream(t *testing.T) {
	frames := make(chan protocol.SubResponse)
	subscribeCtxs := make(chan context.Context, 1)
	conn := startIngressServer(t, ingressAppCtx(t, streamingConnector(t, frames, subscribeCtxs)), nil)
	ctx, cancel := context.WithCancel(suiIngressContext(t))
	go func() { frames <- &protocol.GrpcSubResponse{Message: checkpointFrame(t, 1), UpstreamId: "id"} }()

	stream := openStream(t, conn, ctx, suiSubscribeCheckpoints, &sui.SubscribeCheckpointsRequest{})
	require.NoError(t, stream.RecvMsg(&sui.SubscribeCheckpointsResponse{}))
	subscribeCtx := <-subscribeCtxs

	cancel()

	select {
	case <-subscribeCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream stream ctx must be cancelled when the client goes away")
	}
}

// The unary path must be untouched by the split: an unknown method is still
// UNIMPLEMENTED even when issued as a stream.
func TestChainIngressUnknownMethodAsStream(t *testing.T) {
	conn := startIngressServer(t, ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector)), nil)

	stream := openStream(t, conn, suiIngressContext(t), "/sui.rpc.v2.LedgerService/Bogus", &sui.ListCheckpointsRequest{})
	err := stream.RecvMsg(&sui.ListCheckpointsResponse{})

	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestChainIngressRequiresChainMetadata(t *testing.T) {
	conn := startIngressServer(t, ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector)), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	err := conn.Invoke(ctx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.ErrorContains(t, err, "X-Nodecore-Chain metadata is required")

	wrongChainCtx := metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "not-a-chain")
	err = conn.Invoke(wrongChainCtx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.ErrorContains(t, err, "chain not-a-chain is not supported")
}

// an unknown emerald.* method must answer UNIMPLEMENTED immediately - the
// namespace belongs to the registered dshackle services and is never proxied
func TestChainIngressGuardsEmeraldNamespace(t *testing.T) {
	authProc := mocks.NewMockAuthProcessor()
	appCtx := server_ctx.NewApplicationServerContext(nil, nil, nil, authProc, nil, nil, nil, nil, nil, nil)
	conn := startIngressServer(t, appCtx, nil)
	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-nodecore-chain", "sui")

	err := conn.Invoke(ctx, "/emerald.Bogus/Method", &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})

	assert.Equal(t, codes.Unimplemented, status.Code(err))
	authProc.AssertNotCalled(t, "Authenticate")
}

// stubAuthServer is a real generated-stub service: registering it next to
// the raw catch-all proves the delegating codec's proto path.
type stubAuthServer struct {
	dshackle.UnimplementedAuthServer
}

func (stubAuthServer) Authenticate(_ context.Context, request *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
	return &dshackle.AuthResponse{ProviderToken: "echo:" + request.GetToken()}, nil
}

// A generated service registered next to the raw catch-all (in production:
// the reflection service) keeps working through the delegating codec's proto
// path.
func TestChainIngressKeepsRegisteredServicesWorking(t *testing.T) {
	appCtx := ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector))
	conn := startIngressServer(t, appCtx, func(server *grpc.Server) {
		dshackle.RegisterAuthServer(server, stubAuthServer{})
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	// a round-tripped payload - not the ingress's "unknown method" and not a
	// codec/transport failure - proves the proto path works
	response, err := dshackle.NewAuthClient(conn).Authenticate(ctx, &dshackle.AuthRequest{Token: "any"})
	require.NoError(t, err)
	assert.Equal(t, "echo:any", response.GetProviderToken())
}

func reflectionRoundTrip(t *testing.T, conn *grpc.ClientConn, request *v1reflectionpb.ServerReflectionRequest) *v1reflectionpb.ServerReflectionResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	stream, err := v1reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(request))
	response, err := stream.Recv()
	require.NoError(t, err)
	return response
}

// reflection must advertise every spec-declared chain service, so tools like
// Postman and `grpcurl list` discover the ingress surface natively
func TestChainIngressReflectionListsChainServices(t *testing.T) {
	conn := startIngressServer(t, ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector)), nil)

	response := reflectionRoundTrip(t, conn, &v1reflectionpb.ServerReflectionRequest{
		MessageRequest: &v1reflectionpb.ServerReflectionRequest_ListServices{},
	})

	services := make([]string, 0)
	for _, service := range response.GetListServicesResponse().GetService() {
		services = append(services, service.GetName())
	}
	for _, expected := range []string{
		"sui.rpc.v2.LedgerService",
		"sui.rpc.v2.StateService",
		"sui.rpc.v2.MovePackageService",
		"sui.rpc.v2.TransactionExecutionService",
		"sui.rpc.v2.SignatureVerificationService",
		"sui.rpc.v2.NameService",
		"sui.rpc.v2.SubscriptionService",
		"grpc.reflection.v1.ServerReflection",
	} {
		assert.Contains(t, services, expected)
	}
}

// symbol lookup must serve complete descriptors for every advertised service -
// this is what grpcurl/Postman encode real requests against
func TestChainIngressReflectionServesChainSymbols(t *testing.T) {
	conn := startIngressServer(t, ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector)), nil)

	response := reflectionRoundTrip(t, conn, &v1reflectionpb.ServerReflectionRequest{
		MessageRequest: &v1reflectionpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: "sui.rpc.v2.SignatureVerificationService",
		},
	})

	require.Nil(t, response.GetErrorResponse(), "symbol must resolve: %v", response.GetErrorResponse())
	assert.NotEmpty(t, response.GetFileDescriptorResponse().GetFileDescriptorProto())
}

// realKeyAuthProcessor builds a real basic auth processor with one local key
// and no request strategy - the shape the ingress runs with when key auth is
// configured.
func realKeyAuthProcessor(t *testing.T) auth.AuthProcessor {
	t.Helper()
	authProc, err := auth.NewAuthProcessor(t.Context(), &config.AuthConfig{
		Enabled: true,
		KeyConfigs: []*config.KeyConfig{
			{
				Id:   "k1",
				Type: config.Local,
				LocalKeyConfig: &config.LocalKeyConfig{
					Key:               "secret-key",
					KeySettingsConfig: &config.KeySettingsConfig{},
				},
			},
		},
	}, integration.NewIntegrationResolver(nil))
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	return authProc
}

// end-to-end key auth: the X-Nodecore-Key metadata must authenticate against
// a real auth processor, and its absence or a wrong value must be rejected
// before anything reaches an upstream
func TestChainIngressAuthViaXNodecoreKey(t *testing.T) {
	respBytes, err := proto.Marshal(&sui.GetServiceInfoResponse{Chain: new("mainnet")})
	require.NoError(t, err)
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewGrpcUpstreamResponse("1", respBytes)).Once()

	conn := startIngressServer(t, ingressAppCtxWithAuth(t, connector, realKeyAuthProcessor(t)), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	chainCtx := metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "sui")

	// no key at all
	err = conn.Invoke(chainCtx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.ErrorContains(t, err, "api-key must be provided")

	// wrong key
	wrongKeyCtx := metadata.AppendToOutgoingContext(chainCtx, "x-nodecore-key", "nope")
	err = conn.Invoke(wrongKeyCtx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.ErrorContains(t, err, "specified api-key not found")

	// valid key reaches the upstream
	var reply sui.GetServiceInfoResponse
	validCtx := metadata.AppendToOutgoingContext(chainCtx, "x-nodecore-key", "secret-key")
	require.NoError(t, conn.Invoke(validCtx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &reply))
	assert.Equal(t, "mainnet", reply.GetChain())
	connector.AssertExpectations(t)
}

// end-to-end token auth: the X-Nodecore-Token metadata must authenticate
// against a real token-strategy processor
func TestChainIngressAuthViaXNodecoreToken(t *testing.T) {
	authProc, err := auth.NewAuthProcessor(t.Context(), &config.AuthConfig{
		Enabled: true,
		RequestStrategyConfig: &config.RequestStrategyConfig{
			Type:                       config.Token,
			TokenRequestStrategyConfig: &config.TokenRequestStrategyConfig{Value: "super-secret"},
		},
	}, integration.NewIntegrationResolver(nil))
	require.NoError(t, err)

	respBytes, err := proto.Marshal(&sui.GetServiceInfoResponse{Chain: new("mainnet")})
	require.NoError(t, err)
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	connector.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewGrpcUpstreamResponse("1", respBytes)).Once()

	conn := startIngressServer(t, ingressAppCtxWithAuth(t, connector, authProc), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	chainCtx := metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "sui")

	err = conn.Invoke(chainCtx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.ErrorContains(t, err, "invalid secret token")

	var reply sui.GetServiceInfoResponse
	validCtx := metadata.AppendToOutgoingContext(chainCtx, "x-nodecore-token", "super-secret")
	require.NoError(t, conn.Invoke(validCtx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &reply))
	assert.Equal(t, "mainnet", reply.GetChain())
	connector.AssertExpectations(t)
}

// allowed-ips keys over the ingress: the peer IP is resolved into the context
// (bufconn's unparseable peer address falls back to 127.0.0.1), so an
// ip-scoped key denies or passes - and never panics the process
func TestChainIngressKeyAllowedIps(t *testing.T) {
	ipScopedProcessor := func(allowedIp string) auth.AuthProcessor {
		authProc, err := auth.NewAuthProcessor(t.Context(), &config.AuthConfig{
			Enabled: true,
			KeyConfigs: []*config.KeyConfig{
				{
					Id:   "k1",
					Type: config.Local,
					LocalKeyConfig: &config.LocalKeyConfig{
						Key:               "secret-key",
						KeySettingsConfig: &config.KeySettingsConfig{AllowedIps: []string{allowedIp}},
					},
				},
			},
		}, integration.NewIntegrationResolver(nil))
		require.NoError(t, err)
		time.Sleep(50 * time.Millisecond)
		return authProc
	}
	call := func(authProc auth.AuthProcessor) error {
		connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
		respBytes, err := proto.Marshal(&sui.GetServiceInfoResponse{Chain: new("mainnet")})
		require.NoError(t, err)
		connector.On("SendRequest", mock.Anything, mock.Anything).
			Return(protocol.NewGrpcUpstreamResponse("1", respBytes)).Maybe()
		conn := startIngressServer(t, ingressAppCtxWithAuth(t, connector, authProc), nil)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		t.Cleanup(cancel)
		ctx = metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "sui", "x-nodecore-key", "secret-key")
		return conn.Invoke(ctx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{})
	}

	err := call(ipScopedProcessor("10.9.9.9"))
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.ErrorContains(t, err, "not allowed")

	assert.NoError(t, call(ipScopedProcessor("127.0.0.1")))
}

// a live client that opens a call and never sends its request message must be
// bounded by the first-message deadline, not held forever
func TestChainIngressSilentStreamIsBounded(t *testing.T) {
	previous := firstMessageDeadline
	firstMessageDeadline = 300 * time.Millisecond
	t.Cleanup(func() { firstMessageDeadline = previous })

	conn := startIngressServer(t, ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector)), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	ctx = metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "sui")

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{
		StreamName:    "GetServiceInfo",
		ClientStreams: true,
		ServerStreams: true,
	}, suiGetServiceInfo)
	require.NoError(t, err)

	// never send; the server must close the call with DEADLINE_EXCEEDED
	err = stream.RecvMsg(&sui.GetServiceInfoResponse{})
	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	assert.ErrorContains(t, err, "no request message received")
}

// receive-side errors keep their gRPC identity: an oversized request message
// must surface as RESOURCE_EXHAUSTED, not INTERNAL
func TestChainIngressOversizedRequestIsResourceExhausted(t *testing.T) {
	conn := startIngressServer(t, ingressAppCtx(t, mocks.NewConnectorMockWithType(specs.GrpcConnector)), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	ctx = metadata.AppendToOutgoingContext(ctx, "x-nodecore-chain", "sui")

	// >4MB (the server-side default receive limit), sent as an opaque payload
	big := &dshackle.AuthRequest{Token: strings.Repeat("a", 5<<20)}
	err := conn.Invoke(ctx, suiGetServiceInfo, big, &sui.GetServiceInfoResponse{})

	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}

// nodecore's credential/routing metadata is consumed by the ingress and must
// never enter the request holder; everything else is forwarded to the upstream
func TestChainIngressStripsNodecoreMetadataBeforeForwarding(t *testing.T) {
	var forwarded map[string][]string
	connector := mocks.NewConnectorMockWithType(specs.GrpcConnector)
	respBytes, err := proto.Marshal(&sui.GetServiceInfoResponse{Chain: new("mainnet")})
	require.NoError(t, err)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		forwarded = r.RequestParams().Headers
		return true
	})).Return(protocol.NewGrpcUpstreamResponse("1", respBytes)).Once()

	conn := startIngressServer(t, ingressAppCtxWithAuth(t, connector, realKeyAuthProcessor(t)), nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	ctx = metadata.AppendToOutgoingContext(ctx,
		"x-nodecore-chain", "sui",
		"x-nodecore-key", "secret-key",
		"x-nodecore-token", "some-token",
		"authorization", "Bearer some-jwt",
		"x-custom-meta", "keep-me",
	)

	require.NoError(t, conn.Invoke(ctx, suiGetServiceInfo, &sui.GetServiceInfoRequest{}, &sui.GetServiceInfoResponse{}))
	connector.AssertExpectations(t)

	md := metadata.MD(forwarded)
	assert.Equal(t, []string{"keep-me"}, md.Get("x-custom-meta"))
	assert.Empty(t, md.Get("x-nodecore-key"))
	assert.Empty(t, md.Get("x-nodecore-token"))
	assert.Empty(t, md.Get("x-nodecore-chain"))
	assert.Empty(t, md.Get("authorization"))
}

// the response channel closing without a wrapper must never yield a nil
// status (OK with no message -> bare io.EOF at the client)
func TestStatusFromMissingResponse(t *testing.T) {
	err := statusFromMissingResponse(t.Context())
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.ErrorContains(t, err, "no response from the execution flow")

	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	err = statusFromMissingResponse(canceledCtx)
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
}
