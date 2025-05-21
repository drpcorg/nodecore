package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/caches"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/server"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	_ "github.com/drpcorg/dsheltie/pkg/chains"
	_ "github.com/drpcorg/dsheltie/pkg/logger"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/rs/zerolog/log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {

	flag.Parse()

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}
	err = specs.Load()
	if err != nil {
		log.Panic().Err(err).Msg("unable to load method specs")
	}

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())

	upstreamSupervisor := upstreams.NewBaseUpstreamSupervisor(mainCtx, appConfig.UpstreamConfig)
	go upstreamSupervisor.StartUpstreams()

	cacheProcessor := caches.NewCacheProcessor(upstreamSupervisor, appConfig.CacheConfig, 1*time.Second)

	appCtx := server.NewApplicationContext(upstreamSupervisor, cacheProcessor)

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
