package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/server"
	"github.com/drpcorg/dshaltie/internal/upstreams"
	_ "github.com/drpcorg/dshaltie/pkg/chains"
	_ "github.com/drpcorg/dshaltie/pkg/logger"
	"github.com/rs/zerolog/log"
	"os"
	"os/signal"
	"syscall"
)

func main() {

	flag.Parse()

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())

	upstreamSupervisor := upstreams.NewBaseUpstreamSupervisor(mainCtx, appConfig.UpstreamConfig)
	go upstreamSupervisor.StartUpstreams()

	appCtx := server.NewApplicationContext(upstreamSupervisor)

	httpServer := server.NewHttpServer(mainCtx, appCtx)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Info().Msgf("got signal %v", sig)
		mainCtxCancel()
	}()

	go func() {
		if err = httpServer.Start(fmt.Sprintf(":%d", appConfig.ServerConfig.Port)); err != nil {
			log.Panic().Err(err).Msg("http server couldn't start")
		}
	}()

	<-mainCtx.Done()
}
