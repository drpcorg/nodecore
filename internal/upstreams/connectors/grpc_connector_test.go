package connectors_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

// authServerStub is a real generated-stub server: the connector's raw bytes
// must decode on the peer side as normal protobuf.
type authServerStub struct {
	dshackle.UnimplementedAuthServer
	handler func(ctx context.Context, request *dshackle.AuthRequest) (*dshackle.AuthResponse, error)
}

func (a *authServerStub) Authenticate(ctx context.Context, request *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
	return a.handler(ctx, request)
}

func startGrpcConnector(t *testing.T, connectorConfig *config.ApiConnectorConfig, stub *authServerStub) *connectors.GrpcConnector {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	dshackle.RegisterAuthServer(server, stub)
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
	connector := connectors.NewGrpcConnectorWithClientConn(conn, connectorConfig, "up-id")
	t.Cleanup(func() {
		connector.Stop()
		server.Stop()
	})
	return connector
}

func grpcAuthRequest(t *testing.T, token string, headers map[string][]string) protocol.RequestHolder {
	t.Helper()
	body, err := proto.Marshal(&dshackle.AuthRequest{Token: token})
	require.NoError(t, err)
	var params *protocol.RequestParams
	if headers != nil {
		params = &protocol.RequestParams{Headers: headers}
	}
	return protocol.NewUpstreamGrpcRequest("1", dshackle.Auth_Authenticate_FullMethodName, params, body, "")
}

func TestGrpcConnectorUnaryCall(t *testing.T) {
	stub := &authServerStub{handler: func(_ context.Context, request *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
		return &dshackle.AuthResponse{ProviderToken: "echo:" + request.Token}, nil
	}}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, stub)

	response := connector.SendRequest(t.Context(), grpcAuthRequest(t, "my-token", nil))

	require.False(t, response.HasError(), "unexpected error: %v", response.GetError())
	var authResponse dshackle.AuthResponse
	require.NoError(t, proto.Unmarshal(response.ResponseResult(), &authResponse))
	assert.Equal(t, "echo:my-token", authResponse.ProviderToken)
	assert.Equal(t, specs.GrpcConnector, connector.GetType())
}

func TestGrpcConnectorForwardsMetadataWithDenyList(t *testing.T) {
	var received metadata.MD
	stub := &authServerStub{handler: func(ctx context.Context, _ *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
		received, _ = metadata.FromIncomingContext(ctx)
		return &dshackle.AuthResponse{}, nil
	}}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{
		Url:     "grpc://bufnet",
		Headers: map[string]string{"X-Api-Key": "config-secret"},
	}, stub)

	response := connector.SendRequest(t.Context(), grpcAuthRequest(t, "t", map[string][]string{
		"x-custom-meta":  {"a", "b"},
		"grpc-timeout":   {"1S"},    // reserved grpc-* family
		"Connection":     {"close"}, // hop-by-hop
		"x-api-key":      {"client-override"},
		"x-trace-id-bin": {"\x01\x02"},
	}))

	require.False(t, response.HasError(), "unexpected error: %v", response.GetError())
	assert.Equal(t, []string{"a", "b"}, received.Get("x-custom-meta"))
	assert.Equal(t, []string{"\x01\x02"}, received.Get("x-trace-id-bin"))
	assert.Equal(t, []string{"config-secret"}, received.Get("x-api-key"), "config headers must not be overridable by the client")
	assert.Empty(t, received.Get("connection"))
	// grpc-timeout must be the transport's own, never the client's literal value
	assert.NotContains(t, received.Get("grpc-timeout"), "1S")
}

func TestGrpcConnectorClientErrorPassesThroughWithoutRetry(t *testing.T) {
	stub := &authServerStub{handler: func(_ context.Context, _ *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
		st, err := status.New(codes.NotFound, "object not found").
			WithDetails(&errdetails.ErrorInfo{Reason: "OBJECT_PRUNED", Domain: "sui.io"})
		require.NoError(t, err)
		return nil, st.Err()
	}}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, stub)

	response := connector.SendRequest(t.Context(), grpcAuthRequest(t, "t", nil))

	require.True(t, response.HasError())
	assert.False(t, protocol.IsRetryable(response))
	grpcStatus, ok := protocol.GrpcStatusFromError(response.GetError())
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, grpcStatus.Code)
	assert.Equal(t, "object not found", grpcStatus.Message)

	// the verbatim status (with typed details) must ride through
	require.NotEmpty(t, grpcStatus.StatusProto)
	reconstructed := status.FromProto(mustUnmarshalStatus(t, grpcStatus.StatusProto))
	assert.Equal(t, codes.NotFound, reconstructed.Code())
	require.Len(t, reconstructed.Details(), 1)
	errorInfo, ok := reconstructed.Details()[0].(*errdetails.ErrorInfo)
	require.True(t, ok)
	assert.Equal(t, "OBJECT_PRUNED", errorInfo.Reason)
}

func TestGrpcConnectorTransientErrorIsRetryable(t *testing.T) {
	stub := &authServerStub{handler: func(_ context.Context, _ *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
		return nil, status.Error(codes.Unavailable, "node is overloaded")
	}}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, stub)

	response := connector.SendRequest(t.Context(), grpcAuthRequest(t, "t", nil))

	require.True(t, response.HasError())
	assert.True(t, protocol.IsRetryable(response))
	replyError, ok := response.(*protocol.ReplyError)
	require.True(t, ok)
	assert.Equal(t, protocol.PartialFailure, replyError.ErrorKind)
	grpcStatus, ok := protocol.GrpcStatusFromError(response.GetError())
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, grpcStatus.Code)
	assert.Empty(t, grpcStatus.StatusProto, "no typed details - nothing to serialize")
}

func TestGrpcConnectorResponseMetadataPassthrough(t *testing.T) {
	stub := &authServerStub{handler: func(ctx context.Context, _ *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
		require.NoError(t, grpc.SetHeader(ctx, metadata.Pairs(
			"x-chain-meta", "checkpoint-42",
			"set-cookie", "leak",
		)))
		_ = grpc.SetTrailer(ctx, metadata.Pairs(
			"x-ratelimit-remaining", "17",
			"x-denied-by-operator", "leak",
		))
		return &dshackle.AuthResponse{}, nil
	}}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{
		Url:                "grpc://bufnet",
		ResponseHeaderDeny: []string{"X-Denied-By-Operator"},
	}, stub)

	response := connector.SendRequest(t.Context(), grpcAuthRequest(t, "t", nil))
	require.False(t, response.HasError(), "unexpected error: %v", response.GetError())

	headerBearer, ok := response.(protocol.HasResponseHeaders)
	require.True(t, ok)
	// gRPC metadata keys stay lowercase, so read them through metadata.MD
	headers := metadata.MD(headerBearer.ResponseHeaders())
	assert.Equal(t, []string{"checkpoint-42"}, headers.Get("x-chain-meta"))
	assert.NotContains(t, headers, "set-cookie")
	assert.NotContains(t, headers, "content-type")

	trailerBearer, ok := response.(protocol.HasResponseTrailers)
	require.True(t, ok)
	trailers := trailerBearer.ResponseTrailers()
	assert.Equal(t, []string{"17"}, trailers["x-ratelimit-remaining"])
	assert.NotContains(t, trailers, "x-denied-by-operator")
	assert.NotContains(t, trailers, "grpc-status")
}

// SubscribeStates stays nil in this version: ClientConn connectivity states are
// not mapped onto connector-state events.
func TestGrpcConnectorSubscribeStatesIsNil(t *testing.T) {
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, &authServerStub{})

	assert.Nil(t, connector.SubscribeStates("any"))
}

func mustUnmarshalStatus(t *testing.T, data []byte) *spb.Status {
	t.Helper()
	var st spb.Status
	require.NoError(t, proto.Unmarshal(data, &st))
	return &st
}

// a proxy must not impose a lower response-size ceiling than the node itself
// allows - grpc-go's default 4MB receive cap is lifted per call
func TestGrpcConnectorReceivesResponsesAboveDefaultGrpcLimit(t *testing.T) {
	bigToken := strings.Repeat("a", 5<<20)
	stub := &authServerStub{handler: func(_ context.Context, _ *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
		return &dshackle.AuthResponse{ProviderToken: bigToken}, nil
	}}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, stub)

	response := connector.SendRequest(t.Context(), grpcAuthRequest(t, "t", nil))

	require.False(t, response.HasError(), "unexpected error: %v", response.GetError())
	assert.Greater(t, len(response.ResponseResult()), 4<<20)
}

// error replies carry the upstream's filtered metadata too - RESOURCE_EXHAUSTED
// trailers (rate-limit hints) are exactly the ones a client needs
func TestGrpcConnectorRetryableErrorKeepsResponseMetadata(t *testing.T) {
	stub := &authServerStub{handler: func(ctx context.Context, _ *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
		require.NoError(t, grpc.SetHeader(ctx, metadata.Pairs("x-up-meta", "hv")))
		_ = grpc.SetTrailer(ctx, metadata.Pairs("x-ratelimit-reset", "42", "grpc-status-details-bin", "leak"))
		return nil, status.Error(codes.ResourceExhausted, "rate limited")
	}}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, stub)

	response := connector.SendRequest(t.Context(), grpcAuthRequest(t, "t", nil))

	replyError, ok := response.(*protocol.ReplyError)
	require.True(t, ok)
	assert.True(t, protocol.IsRetryable(response))

	headers := metadata.MD(replyError.ResponseHeaders())
	assert.Equal(t, []string{"hv"}, headers.Get("x-up-meta"))
	trailers := replyError.ResponseTrailers()
	assert.Equal(t, []string{"42"}, trailers["x-ratelimit-reset"])
	assert.NotContains(t, trailers, "grpc-status-details-bin", "reserved grpc-* keys stay filtered")
}

// streamHandler serves every method of the test server through
// grpc.UnknownServiceHandler, so the tests can use the real Sui method names
// (whose specs carry the call type) while producing frames with ordinary
// protobuf messages on the peer side.
type streamHandler func(stream grpc.ServerStream) error

const (
	suiListCheckpoints      = "/sui.rpc.v2.LedgerService/ListCheckpoints"
	suiSubscribeCheckpoints = "/sui.rpc.v2.SubscriptionService/SubscribeCheckpoints"
)

func startGrpcStreamConnector(t *testing.T, connectorConfig *config.ApiConnectorConfig, handler streamHandler) *connectors.GrpcConnector {
	t.Helper()
	specs_utils.LoadMethodSpecs()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		return handler(stream)
	}))
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
	connector := connectors.NewGrpcConnectorWithClientConn(conn, connectorConfig, "up-id")
	t.Cleanup(func() {
		connector.Stop()
		server.Stop()
	})
	return connector
}

// grpcStreamRequest builds a request for a real Sui streaming method; the body
// is a dshackle.Chain the test server decodes back (payload types are opaque to
// the connector).
func grpcStreamRequest(t *testing.T, method string, headers map[string][]string) protocol.RequestHolder {
	t.Helper()
	body, err := proto.Marshal(&dshackle.Chain{Type: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET})
	require.NoError(t, err)
	var params *protocol.RequestParams
	if headers != nil {
		params = &protocol.RequestParams{Headers: headers}
	}
	return protocol.NewUpstreamGrpcRequest("1", method, params, body, "sui")
}

func recvChain(t *testing.T, stream grpc.ServerStream) *dshackle.Chain {
	t.Helper()
	var request dshackle.Chain
	require.NoError(t, stream.RecvMsg(&request))
	return &request
}

// collectFrames drains the stream until the channel closes or the deadline hits.
func collectFrames(t *testing.T, response protocol.UpstreamSubscriptionResponse) []protocol.SubResponse {
	t.Helper()
	var frames []protocol.SubResponse
	timeout := time.After(5 * time.Second)
	for {
		select {
		case frame, ok := <-response.ResponseChan():
			if !ok {
				return frames
			}
			frames = append(frames, frame)
		case <-timeout:
			t.Fatalf("stream did not close, got %d frames", len(frames))
		}
	}
}

func decodeHead(t *testing.T, frame protocol.SubResponse) uint64 {
	t.Helper()
	var head dshackle.ChainHead
	require.NoError(t, proto.Unmarshal(frame.GetMessage(), &head))
	return head.Height
}

func TestGrpcConnectorServerStreamFiniteDeliversFramesThenCloses(t *testing.T) {
	connector := startGrpcStreamConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, func(stream grpc.ServerStream) error {
		recvChain(t, stream)
		require.NoError(t, stream.SetHeader(metadata.Pairs("x-up-meta", "h", "content-type", "x")))
		stream.SetTrailer(metadata.Pairs("x-up-trailer", "t", "grpc-status-details-bin", "x"))
		for height := uint64(1); height <= 3; height++ {
			if err := stream.SendMsg(&dshackle.ChainHead{Height: height}); err != nil {
				return err
			}
		}
		return nil
	})

	response, err := connector.Subscribe(t.Context(), grpcStreamRequest(t, suiListCheckpoints, nil))
	require.NoError(t, err)
	frames := collectFrames(t, response)

	require.Len(t, frames, 4, "three data frames, then the end frame")
	for i, frame := range frames[:3] {
		assert.Nil(t, frame.GetError())
		assert.False(t, frame.IsEnd())
		assert.Equal(t, "up-id", frame.GetUpstreamId())
		assert.Equal(t, uint64(i+1), decodeHead(t, frame))
	}
	first := frames[0].(*protocol.GrpcSubResponse)
	end := frames[3].(*protocol.GrpcSubResponse)
	// gRPC metadata keys stay lowercase (not http canonical form)
	firstHeaders := map[string][]string(first.ResponseHeaders())
	assert.Equal(t, []string{"h"}, firstHeaders["x-up-meta"])
	assert.Empty(t, firstHeaders["content-type"], "transport-owned metadata is filtered")
	assert.Nil(t, frames[1].(*protocol.GrpcSubResponse).ResponseHeaders(), "headers ride on the first frame only")
	assert.Nil(t, first.ResponseTrailers())
	assert.True(t, end.IsEnd())
	assert.Nil(t, end.GetError())
	assert.Nil(t, end.GetMessage())
	assert.Equal(t, "up-id", end.GetUpstreamId())
	assert.Equal(t, []string{"t"}, end.ResponseTrailers()["x-up-trailer"])
	assert.Empty(t, end.ResponseTrailers()["grpc-status-details-bin"], "the grpc-* family is filtered")
}

// A stream that ends before any data frame still delivers headers - on the end
// frame, since there is no first data frame to carry them.
func TestGrpcConnectorServerStreamEmptyFiniteStream(t *testing.T) {
	connector := startGrpcStreamConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, func(stream grpc.ServerStream) error {
		recvChain(t, stream)
		require.NoError(t, stream.SetHeader(metadata.Pairs("x-up-meta", "h")))
		stream.SetTrailer(metadata.Pairs("x-up-trailer", "t"))
		return nil
	})

	response, err := connector.Subscribe(t.Context(), grpcStreamRequest(t, suiListCheckpoints, nil))
	require.NoError(t, err)
	frames := collectFrames(t, response)

	require.Len(t, frames, 1)
	end := frames[0].(*protocol.GrpcSubResponse)
	assert.True(t, end.IsEnd())
	assert.Equal(t, []string{"h"}, map[string][]string(end.ResponseHeaders())["x-up-meta"])
	assert.Equal(t, []string{"t"}, end.ResponseTrailers()["x-up-trailer"])
}

func TestGrpcConnectorServerStreamStatusErrorIsTheTerminalFrame(t *testing.T) {
	connector := startGrpcStreamConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, func(stream grpc.ServerStream) error {
		recvChain(t, stream)
		if err := stream.SendMsg(&dshackle.ChainHead{Height: 1}); err != nil {
			return err
		}
		stream.SetTrailer(metadata.Pairs("x-rate-limit", "0"))
		st, err := status.New(codes.ResourceExhausted, "slow down").
			WithDetails(&errdetails.RetryInfo{})
		require.NoError(t, err)
		return st.Err()
	})

	response, err := connector.Subscribe(t.Context(), grpcStreamRequest(t, suiSubscribeCheckpoints, nil))
	require.NoError(t, err)
	frames := collectFrames(t, response)

	require.Len(t, frames, 2)
	assert.Equal(t, uint64(1), decodeHead(t, frames[0]))
	terminal := frames[1].(*protocol.GrpcSubResponse)
	require.NotNil(t, terminal.GetError())
	grpcStatus, ok := protocol.GrpcStatusFromError(terminal.GetError())
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, grpcStatus.Code)
	assert.Equal(t, "slow down", grpcStatus.Message)
	assert.NotEmpty(t, grpcStatus.StatusProto, "typed details ride along")
	assert.Equal(t, []string{"0"}, terminal.ResponseTrailers()["x-rate-limit"])
	assert.Equal(t, "up-id", terminal.GetUpstreamId())
}

// A server that rejects the call before sending anything still answers through
// the receive path: one error frame, then the channel closes.
func TestGrpcConnectorServerStreamRejectedByServer(t *testing.T) {
	connector := startGrpcStreamConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, func(grpc.ServerStream) error {
		return status.Error(codes.Unimplemented, "subscriptions disabled")
	})

	response, err := connector.Subscribe(t.Context(), grpcStreamRequest(t, suiSubscribeCheckpoints, nil))
	require.NoError(t, err)
	frames := collectFrames(t, response)

	require.Len(t, frames, 1)
	grpcStatus, ok := protocol.GrpcStatusFromError(frames[0].GetError())
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, grpcStatus.Code)
}

func TestGrpcConnectorServerStreamCancelStopsTheUpstreamStream(t *testing.T) {
	serverDone := make(chan struct{})
	connector := startGrpcStreamConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, func(stream grpc.ServerStream) error {
		defer close(serverDone)
		recvChain(t, stream)
		if err := stream.SendMsg(&dshackle.ChainHead{Height: 1}); err != nil {
			return err
		}
		<-stream.Context().Done()
		return stream.Context().Err()
	})
	ctx, cancel := context.WithCancel(t.Context())

	// a subscription frame is forwarded at once - it must not wait for the next one
	response, err := connector.Subscribe(ctx, grpcStreamRequest(t, suiSubscribeCheckpoints, nil))
	require.NoError(t, err)
	first := <-response.ResponseChan()
	assert.Equal(t, uint64(1), decodeHead(t, first))

	cancel()

	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream stream was not cancelled")
	}
	for range response.ResponseChan() {
		t.Fatal("no frame is reported once the caller gave up")
	}
}

func TestGrpcConnectorServerStreamForwardsBodyAndMetadata(t *testing.T) {
	var received metadata.MD
	var receivedChain dshackle.ChainRef
	connector := startGrpcStreamConnector(t, &config.ApiConnectorConfig{
		Url:     "grpc://bufnet",
		Headers: map[string]string{"X-Api-Key": "config-secret"},
	}, func(stream grpc.ServerStream) error {
		received, _ = metadata.FromIncomingContext(stream.Context())
		receivedChain = recvChain(t, stream).Type
		return nil
	})

	response, err := connector.Subscribe(t.Context(), grpcStreamRequest(t, suiListCheckpoints, map[string][]string{
		"x-custom-meta": {"a"},
		"grpc-timeout":  {"1S"},
		"x-api-key":     {"client-override"},
	}))
	require.NoError(t, err)
	collectFrames(t, response)

	assert.Equal(t, dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET, receivedChain)
	assert.Equal(t, []string{"a"}, received.Get("x-custom-meta"))
	assert.Equal(t, []string{"config-secret"}, received.Get("x-api-key"))
	assert.Empty(t, received.Get("grpc-timeout"))
}

func TestGrpcConnectorUnsubscribeIsANoOp(t *testing.T) {
	connector := startGrpcStreamConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, func(grpc.ServerStream) error { return nil })
	connector.Unsubscribe("anything")
}
