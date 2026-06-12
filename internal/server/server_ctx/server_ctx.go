package server_ctx

import (
	"github.com/drpcorg/nodecore/internal/auth"
	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/stats"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
)

type ApplicationServerContext struct {
	UpstreamSupervisor upstreams.UpstreamSupervisor
	CacheProcessor     caches.CacheProcessor
	Registry           *rating.RatingRegistry
	AuthProcessor      auth.AuthProcessor
	AppConfig          *config.AppConfig
	StorageRegistry    *storages.StorageRegistry
	StatsService       stats.StatsService
	DimensionTracker   dimensions.DimensionTracker
	QuorumRegistry     *quorum.Registry
	SubEngineRegistry  *subengine.Registry
}

func NewApplicationServerContext(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheProcessor caches.CacheProcessor,
	registry *rating.RatingRegistry,
	authProcessor auth.AuthProcessor,
	appConfig *config.AppConfig,
	storageRegistry *storages.StorageRegistry,
	statsService stats.StatsService,
	dimensionTracker dimensions.DimensionTracker,
	quorumRegistry *quorum.Registry,
	subEngineRegistry *subengine.Registry,
) *ApplicationServerContext {
	return &ApplicationServerContext{
		UpstreamSupervisor: upstreamSupervisor,
		CacheProcessor:     cacheProcessor,
		Registry:           registry,
		AuthProcessor:      authProcessor,
		AppConfig:          appConfig,
		StorageRegistry:    storageRegistry,
		StatsService:       statsService,
		DimensionTracker:   dimensionTracker,
		QuorumRegistry:     quorumRegistry,
		SubEngineRegistry:  subEngineRegistry,
	}
}
