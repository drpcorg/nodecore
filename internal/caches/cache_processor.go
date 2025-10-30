package caches

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/storages"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

var requestCache = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "request",
		Name:      "cache_hit",
		Help:      "The total number of RPC requests served from cache",
	},
	[]string{"chain", "method"},
)

func init() {
	prometheus.MustRegister(requestCache)
}

type CacheProcessor interface {
	Store(ctx context.Context, chain chains.Chain, request protocol.RequestHolder, response []byte)
	Receive(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) ([]byte, bool)
}

type BaseCacheProcessor struct {
	policies       []*CachePolicy
	receiveTimeout time.Duration
}

func NewBaseCacheProcessor(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheConfig *config.CacheConfig,
	storageRegistry *storages.StorageRegistry,
) (*BaseCacheProcessor, error) {
	cacheConnectors := make([]CacheConnector, 0)

	for _, connectorCfg := range cacheConfig.CacheConnectors {
		var connector CacheConnector
		var err error
		switch connectorCfg.Driver {
		case config.Memory:
			connector, err = NewInMemoryConnector(connectorCfg.Id, connectorCfg.Memory)
		case config.Redis:
			connector, err = NewRedisConnector(connectorCfg.Id, connectorCfg.Redis, storageRegistry)
		case config.Postgres:
			connector, err = NewPostgresConnector(connectorCfg.Id, connectorCfg.Postgres, storageRegistry)
		default:
			return nil, fmt.Errorf("unknown connector driver '%s'", connectorCfg.Driver)
		}
		if err != nil {
			return nil, err
		}
		cacheConnectors = append(cacheConnectors, connector)
	}

	for _, connector := range cacheConnectors {
		if err := connector.Initialize(); err != nil {
			return nil, err
		}
	}

	cachePolicies := lo.FilterMap(cacheConfig.CachePolicies, func(item *config.CachePolicyConfig, index int) (*CachePolicy, bool) {
		connector, ok := lo.Find(cacheConnectors, func(conn CacheConnector) bool {
			return item.Connector == conn.Id()
		})
		if ok {
			log.Info().Msgf("%s cache policy with %s connector will be used to cache responses", item.Id, connector.Id())
			return NewCachePolicy(upstreamSupervisor, connector, item), true
		}
		return nil, false
	})

	return &BaseCacheProcessor{
		receiveTimeout: cacheConfig.ReceiveTimeout,
		policies:       cachePolicies,
	}, nil
}

func (c *BaseCacheProcessor) Store(
	ctx context.Context,
	chain chains.Chain,
	request protocol.RequestHolder,
	response []byte,
) {
	for _, policy := range c.policies {
		policy.Store(ctx, chain, request, response)
	}
}

func (c *BaseCacheProcessor) Receive(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) ([]byte, bool) {
	if len(c.policies) == 0 {
		return nil, false
	}

	resultSent := atomic.Bool{}
	resultChan := make(chan []byte)

	ctx, cancel := context.WithTimeout(ctx, c.receiveTimeout)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(len(c.policies))

	for _, policy := range c.policies {
		go func(p *CachePolicy) {
			defer wg.Done()
			result, ok := p.Receive(ctx, chain, request)

			if ok && resultSent.CompareAndSwap(false, true) {
				requestCache.WithLabelValues(chain.String(), request.Method()).Inc()
				resultChan <- result
				cancel()
			}
		}(policy)
	}

	// If all policies have responded and none succeeded, cancel early to avoid waiting for the timeout.
	go func() {
		wg.Wait()
		if !resultSent.Load() {
			cancel()
		}
	}()

	var result []byte

	select {
	case <-ctx.Done():
	case cacheResult := <-resultChan:
		result = cacheResult
	}

	return result, len(result) > 0
}
