package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/caches"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/rating"
	"github.com/drpcorg/dsheltie/internal/server"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	_ "github.com/drpcorg/dsheltie/pkg/chains"
	_ "github.com/drpcorg/dsheltie/pkg/errors_config"
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

	dimensionTracker := dimensions.NewDimensionTracker()

	upstreamSupervisor := upstreams.NewBaseUpstreamSupervisor(mainCtx, appConfig.UpstreamConfig, dimensionTracker)
	go upstreamSupervisor.StartUpstreams()

	ratingRegistry := rating.NewRatingRegistry(upstreamSupervisor, dimensionTracker, appConfig.UpstreamConfig.ScorePolicyConfig)
	go ratingRegistry.Start()

	cacheProcessor := caches.NewBaseCacheProcessor(upstreamSupervisor, appConfig.CacheConfig, 1*time.Second)

	appCtx := server.NewApplicationContext(upstreamSupervisor, cacheProcessor, ratingRegistry)

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
