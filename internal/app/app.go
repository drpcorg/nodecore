package app

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/server"
	"github.com/drpcorg/nodecore/internal/stats"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/pyroscope"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
)

type App struct {
	ctx context.Context

	appConfig          *config.AppConfig
	statsService       stats.StatsService
	authProcessor      auth.AuthProcessor
	ratingRegistry     *rating.RatingRegistry
	cacheProcessor     caches.CacheProcessor
	upstreamSupervisor upstreams.UpstreamSupervisor

	httpServer *echo.Echo
}

func NewApp(ctx context.Context, appConfig *config.AppConfig) (*App, error) {
	integrationResolver := integration.NewIntegrationResolver(appConfig.IntegrationConfig)

	authProcessor, err := auth.NewAuthProcessor(ctx, appConfig.AuthConfig, integrationResolver)
	if err != nil {
		return nil, fmt.Errorf("unable to create the auth processor: %w", err)
	}
	storageRegistry, err := storages.NewStorageRegistry(appConfig.AppStorages)
	if err != nil {
		return nil, fmt.Errorf("unable to create the storage registry: %w", err)
	}
	dimensionTracker := dimensions.NewDimensionTracker()
	statsService := stats.NewStatsService(ctx, appConfig.StatsConfig, integrationResolver)
	rateLimitBudgetRegistry, err := ratelimiter.NewRateLimitBudgetRegistry(appConfig.RateLimit, storageRegistry)
	if err != nil {
		return nil, fmt.Errorf("unable to create the rate limit budget registry: %w", err)
	}
	upstreamSupervisor := upstreams.NewBaseUpstreamSupervisor(
		ctx,
		appConfig.UpstreamConfig,
		dimensionTracker,
		statsService,
		rateLimitBudgetRegistry,
		appConfig.ServerConfig.TorUrl,
	)
	ratingRegistry := rating.NewRatingRegistry(upstreamSupervisor, dimensionTracker, appConfig.UpstreamConfig.ScorePolicyConfig)
	cacheProcessor, err := caches.NewBaseCacheProcessor(upstreamSupervisor, appConfig.CacheConfig, storageRegistry)
	if err != nil {
		return nil, fmt.Errorf("unable to create the cache processor: %w", err)
	}

	appCtx := server.NewApplicationContext(
		upstreamSupervisor,
		cacheProcessor,
		ratingRegistry,
		authProcessor,
		appConfig,
		storageRegistry,
		statsService,
		dimensionTracker,
	)

	httpServer := server.NewHttpServer(ctx, appCtx)

	return &App{
		ctx:                ctx,
		appConfig:          appConfig,
		ratingRegistry:     ratingRegistry,
		cacheProcessor:     cacheProcessor,
		authProcessor:      authProcessor,
		statsService:       statsService,
		upstreamSupervisor: upstreamSupervisor,
		httpServer:         httpServer,
	}, nil
}

func (a *App) Start() {
	var shuttingDown atomic.Bool

	go a.upstreamSupervisor.StartUpstreams()
	go a.ratingRegistry.Start()
	a.statsService.Start()

	go func() {
		if a.appConfig.ServerConfig.PprofPort != 0 {
			pprofServer := http.Server{
				Addr: fmt.Sprintf("localhost:%d", a.appConfig.ServerConfig.PprofPort),
			}
			log.Info().Msgf("starting pprof server on port %d", a.appConfig.ServerConfig.PprofPort)
			pprofErr := pprofServer.ListenAndServe()
			if pprofErr != nil {
				log.Error().Err(pprofErr).Msg("pprof server couldn't start")
			}
		} else {
			log.Warn().Msg("pprof server is disabled")
		}
	}()

	if a.appConfig.ServerConfig.PyroscopeConfig.Enabled {
		err := pyroscope.InitPyroscope(fmt.Sprintf("%s-namespace", config.AppName), config.AppName, a.appConfig.ServerConfig.PyroscopeConfig)
		if err != nil {
			log.Warn().Err(err).Msg("error during pyroscope initialization")
		}
	}

	go func() {
		if a.appConfig.ServerConfig.MetricsPort != 0 {
			metricsServer := echo.New()
			metricsServer.HideBanner = true
			metricsServer.Use(echoprometheus.NewMiddleware(config.AppName))
			metricsServer.GET("/metrics", echoprometheus.NewHandler())

			if metricsServerErr := server.StartEcho(metricsServer, fmt.Sprintf(":%d", a.appConfig.ServerConfig.MetricsPort), nil); metricsServerErr != nil {
				log.Panic().Err(metricsServerErr).Msg("metrics server couldn't start")
			}
		} else {
			log.Warn().Msg("metrics server is disabled")
		}
	}()

	go func() {
		if httpServerErr := server.StartEcho(a.httpServer, fmt.Sprintf(":%d", a.appConfig.ServerConfig.Port), a.appConfig.ServerConfig.TlsConfig); httpServerErr != nil {
			if !shuttingDown.Load() {
				log.Panic().Err(httpServerErr).Msg("http server couldn't start")
			}
		}
	}()

	<-a.ctx.Done()
	shuttingDown.Store(true)
	log.Info().Msg("nodecore is shutting down")

	shutDownCtx, shutDownCtxCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutDownCtxCancel()

	err := a.httpServer.Shutdown(shutDownCtx)
	if err != nil {
		log.Error().Err(err).Msg("http server couldn't stop gracefully")
	} else {
		log.Info().Msg("http server stopped gracefully")
	}

	err = a.statsService.Stop(shutDownCtx)
	if err != nil {
		log.Error().Err(err).Msg("stats service couldn't stop gracefully")
	}
}
