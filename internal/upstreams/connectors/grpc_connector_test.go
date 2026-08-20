package connectors_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	specs "github.com/drpcorg/nodecore/pkg/methods"
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

func TestGrpcConnectorSubscribeNotSupported(t *testing.T) {
	stub := &authServerStub{}
	connector := startGrpcConnector(t, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, stub)

	sub, err := connector.Subscribe(t.Context(), grpcAuthRequest(t, "t", nil))

	assert.Nil(t, sub)
	assert.ErrorContains(t, err, "not supported")
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
