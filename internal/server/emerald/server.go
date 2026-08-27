package emerald

import (
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/public/pkg/dshackle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// NewServer builds the dshackle grpc.Server: the emerald blockchain/auth
// services and default reflection (which advertises only the registered
// services). baseOptions carry the shared transport options (tls). The chain
// ingress runs as a separate server (internal/server/grpc_ingress) and never
// shares options, codecs or auth with this one.
func NewServer(appCtx *server_ctx.ApplicationServerContext, serverConfig *config.ServerConfig, baseOptions ...grpc.ServerOption) (*grpc.Server, error) {
	grpcServer := grpc.NewServer(baseOptions...)
	reflection.Register(grpcServer)

	authService, sessionAuth, err := NewGrpcAuthService(serverConfig.GrpcAuthConfig)
	if err != nil {
		return nil, err
	}
	responseSigner, err := newResponseSigner(serverConfig.GrpcAuthConfig)
	if err != nil {
		return nil, err
	}
	blockchainService := NewGrpcBlockchainService(appCtx, sessionAuth, responseSigner)

	dshackle.RegisterBlockchainServer(grpcServer, blockchainService)
	if authService != nil {
		dshackle.RegisterAuthServer(grpcServer, authService)
	}

	return grpcServer, nil
}
