package connectors

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// blockingAuthServer never answers - it holds the call until the client's
// deadline fires, so the tests can tell OUR cap from the CALLER's deadline.
type blockingAuthServer struct {
	dshackle.UnimplementedAuthServer
}

func (blockingAuthServer) Authenticate(ctx context.Context, _ *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func newBlockingConnector(t *testing.T) *GrpcConnector {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	dshackle.RegisterAuthServer(server, blockingAuthServer{})
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
	connector := NewGrpcConnectorWithClientConn(conn, &config.ApiConnectorConfig{Url: "grpc://bufnet"}, "up-id")
	t.Cleanup(func() {
		connector.Stop()
		server.Stop()
	})
	return connector
}

// the connector's own cap expiring must stay retry/hedge-eligible - it is the
// upstream being slow, not the caller giving up
func TestGrpcConnectorCapExpiryIsRetryableDeadline(t *testing.T) {
	connector := newBlockingConnector(t)
	connector.requestTimeout = 200 * time.Millisecond

	request := protocol.NewUpstreamGrpcRequest("1", dshackle.Auth_Authenticate_FullMethodName, nil, nil, "")
	response := connector.SendRequest(context.Background(), request)

	require.True(t, response.HasError())
	assert.True(t, protocol.IsRetryable(response))
	grpcStatus, ok := protocol.GrpcStatusFromError(response.GetError())
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, grpcStatus.Code)
}

// the caller's own deadline firing means the caller gave up (probe timeout,
// client disconnect) - a total failure, never retried
func TestGrpcConnectorParentDeadlineIsTotalFailure(t *testing.T) {
	connector := newBlockingConnector(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	request := protocol.NewUpstreamGrpcRequest("1", dshackle.Auth_Authenticate_FullMethodName, nil, nil, "")
	response := connector.SendRequest(ctx, request)

	replyError, ok := response.(*protocol.ReplyError)
	require.True(t, ok)
	assert.Equal(t, protocol.TotalFailure, replyError.ErrorKind)
	assert.Equal(t, protocol.CtxErrorCode, response.GetError().Code)
	assert.False(t, protocol.IsRetryable(response))
}
