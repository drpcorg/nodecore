package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/server"
	"github.com/drpcorg/nodecore/internal/upstreams"
	_ "github.com/drpcorg/nodecore/pkg/chains"
	_ "github.com/drpcorg/nodecore/pkg/errors_config"
	_ "github.com/drpcorg/nodecore/pkg/logger"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/pyroscope"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
	_ "go.uber.org/automaxprocs"
)

func main() {
	flag.Parse()

	appConfig, err := config.NewAppConfig()
	if err != nil {
		log.Panic().Err(err).Msg("unable to parse the config file")
	}
	err = specs.NewMethodSpecLoader().Load()
	if err != nil {
		log.Panic().Err(err).Msg("unable to load method specs")
	}
	scoreConfig := appConfig.UpstreamConfig.ScorePolicyConfig
	if scoreConfig.CalculationFunctionName != "" {
		log.Info().Msgf("the '%s' default score function will be used to calculate rating", scoreConfig.CalculationFunctionName)
	} else if scoreConfig.CalculationFunctionFilePath != "" {
		log.Info().Msgf("the score function from the %s file will be used to calculate rating", scoreConfig.CalculationFunctionFilePath)
	}

	mainCtx, mainCtxCancel := context.WithCancel(context.Background())

	authProcessor, err := auth.NewAuthProcessor(appConfig.AuthConfig)
	if err != nil {
		log.Panic().Err(err).Msg("unable to create the auth processor")
	}

	dimensionTracker := dimensions.NewDimensionTracker()

	upstreamSupervisor := upstreams.NewBaseUpstreamSupervisor(mainCtx, appConfig.UpstreamConfig, dimensionTracker)
	go upstreamSupervisor.StartUpstreams()

	ratingRegistry := rating.NewRatingRegistry(upstreamSupervisor, dimensionTracker, appConfig.UpstreamConfig.ScorePolicyConfig)
	go ratingRegistry.Start()

	cacheProcessor, err := caches.NewBaseCacheProcessor(upstreamSupervisor, appConfig.CacheConfig)
	if err != nil {
		log.Panic().Err(err).Msg("unable to create the cache processor")
	}

	appCtx := server.NewApplicationContext(
		upstreamSupervisor,
		cacheProcessor,
		ratingRegistry,
		authProcessor,
		appConfig,
	)

	httpServer := server.NewHttpServer(mainCtx, appCtx)

	go func() {
		if appConfig.ServerConfig.PprofPort != 0 {
			pprofServer := http.Server{
				Addr: fmt.Sprintf("localhost:%d", appConfig.ServerConfig.PprofPort),
			}
			log.Info().Msgf("starting pprof server on port %d", appConfig.ServerConfig.PprofPort)
			pprofErr := pprofServer.ListenAndServe()
			if pprofErr != nil {
				log.Error().Err(pprofErr).Msg("pprof server couldn't start")
			}
		} else {
			log.Warn().Msg("pprof server is disabled")
		}
	}()

	if appConfig.ServerConfig.PyroscopeConfig.Enabled {
		err = pyroscope.InitPyroscope(fmt.Sprintf("%s-namespace", config.AppName), config.AppName, appConfig.ServerConfig.PyroscopeConfig)
		if err != nil {
			log.Warn().Err(err).Msg("error during pyroscope initialization")
		}
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Info().Msgf("got signal %v", sig)
		mainCtxCancel()
	}()

	go func() {
		if appConfig.ServerConfig.MetricsPort != 0 {
			metricsServer := echo.New()
			metricsServer.HideBanner = true
			metricsServer.Use(echoprometheus.NewMiddleware(config.AppName))
			metricsServer.GET("/metrics", echoprometheus.NewHandler())

			if metricsServerErr := server.StartEcho(metricsServer, fmt.Sprintf(":%d", appConfig.ServerConfig.MetricsPort), nil); metricsServerErr != nil {
				log.Panic().Err(metricsServerErr).Msg("metrics server couldn't start")
			}
		} else {
			log.Warn().Msg("metrics server is disabled")
		}
	}()

	go func() {
		if httpServerErr := server.StartEcho(httpServer, fmt.Sprintf(":%d", appConfig.ServerConfig.Port), appConfig.ServerConfig.TlsConfig); httpServerErr != nil {
			log.Panic().Err(httpServerErr).Msg("http server couldn't start")
		}
	}()

	<-mainCtx.Done()
}
