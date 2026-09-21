package grpc_server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/server/grpc_ingress"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const grpcShutdownTimeout = 10 * time.Second

// grpcEndpoint is one listening grpc.Server. The dshackle server (grpc-port,
// built by internal/server/emerald) and the chain-ingress server
// (grpc-ingress-port, built by internal/server/grpc_ingress) serve completely
// different clients, auth and codecs, so each runs as its own server
// instance; they share only the tls config and the lifecycle below.
type grpcEndpoint struct {
	name     string
	server   *grpc.Server
	listener net.Listener
	port     int
}

type GrpcServer struct {
	endpoints []*grpcEndpoint
}

func NewGrpcServer(appCtx *server_ctx.ApplicationServerContext) (*GrpcServer, error) {
	if appCtx == nil || appCtx.AppConfig == nil || appCtx.AppConfig.ServerConfig == nil {
		return nil, nil
	}
	serverConfig := appCtx.AppConfig.ServerConfig

	baseOptions, err := transportOptions(serverConfig.TlsConfig)
	if err != nil {
		return nil, err
	}

	endpoints := make([]*grpcEndpoint, 0, 2)
	if serverConfig.GrpcPort != 0 {
		server, err := emerald.NewServer(appCtx, serverConfig, baseOptions...)
		if err != nil {
			return nil, err
		}
		endpoint, err := newGrpcEndpoint("dshackle", server, serverConfig.GrpcPort)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	if serverConfig.GrpcIngressPort != 0 {
		endpoint, err := newGrpcEndpoint("chain ingress", grpc_ingress.NewServer(appCtx, baseOptions...), serverConfig.GrpcIngressPort)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	if len(endpoints) == 0 {
		return nil, nil
	}

	return &GrpcServer{endpoints: endpoints}, nil
}

func newGrpcEndpoint(name string, server *grpc.Server, port int) (*grpcEndpoint, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	return &grpcEndpoint{
		name:     name,
		server:   server,
		listener: listener,
		port:     port,
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

	errChan := make(chan error, len(g.endpoints))
	for _, endpoint := range g.endpoints {
		go func() {
			log.Info().Msgf("starting %s grpc server on port %d", endpoint.name, endpoint.port)
			err := endpoint.server.Serve(endpoint.listener)
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				errChan <- fmt.Errorf("%s grpc server failed: %w", endpoint.name, err)
				return
			}
			errChan <- nil
		}()
	}
	for range g.endpoints {
		if err := <-errChan; err != nil {
			return err
		}
	}
	return nil
}

func (g *GrpcServer) shutdown() {
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, endpoint := range g.endpoints {
			wg.Add(1)
			go func() {
				defer wg.Done()
				endpoint.server.GracefulStop()
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(grpcShutdownTimeout):
		for _, endpoint := range g.endpoints {
			endpoint.server.Stop()
		}
	}
}

func transportOptions(tlsConfig *config.TlsConfig) ([]grpc.ServerOption, error) {
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
