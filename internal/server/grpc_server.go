package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const grpcShutdownTimeout = 10 * time.Second

type GrpcServer struct {
	server   *grpc.Server
	listener net.Listener
	port     int
}

func NewGrpcServer(appCtx *ApplicationContext) (*GrpcServer, error) {
	if appCtx == nil || appCtx.appConfig == nil || appCtx.appConfig.ServerConfig == nil {
		return nil, nil
	}
	serverConfig := appCtx.appConfig.ServerConfig
	if serverConfig.GrpcPort == 0 {
		return nil, nil
	}

	options, err := grpcServerOptions(serverConfig.TlsConfig)
	if err != nil {
		return nil, err
	}

	grpcServer := grpc.NewServer(options...)

	authService, sessionAuth, err := NewGrpcAuthService(serverConfig.GrpcAuthConfig)
	if err != nil {
		return nil, err
	}
	blockchainService := NewGrpcBlockchainService(appCtx, sessionAuth)

	dshackle.RegisterBlockchainServer(grpcServer, blockchainService)
	dshackle.RegisterAuthServer(grpcServer, authService)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", serverConfig.GrpcPort))
	if err != nil {
		return nil, err
	}

	return &GrpcServer{
		server:   grpcServer,
		listener: listener,
		port:     serverConfig.GrpcPort,
	}, nil
}

func (g *GrpcServer) Start(mainCtx context.Context) error {
	if g == nil {
		return nil
	}

	go func() {
		<-mainCtx.Done()
		g.shutdown()
	}()

	log.Info().Msgf("starting grpc server on port %d", g.port)
	err := g.server.Serve(g.listener)
	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

func (g *GrpcServer) shutdown() {
	done := make(chan struct{})
	go func() {
		g.server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(grpcShutdownTimeout):
		g.server.Stop()
	}
}

func grpcServerOptions(tlsConfig *config.TlsConfig) ([]grpc.ServerOption, error) {
	options := make([]grpc.ServerOption, 0)
	if tlsConfig != nil && tlsConfig.Enabled {
		creds, err := credentials.NewServerTLSFromFile(tlsConfig.Certificate, tlsConfig.Key)
		if err != nil {
			return nil, err
		}
		options = append(options, grpc.Creds(creds))
	}
	return options, nil
}
