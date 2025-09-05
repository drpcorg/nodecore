package caches

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

var requestCache = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "request",
		Name:      "cache_hit",
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
) *BaseCacheProcessor {
	cacheConnectors := lo.FilterMap(cacheConfig.CacheConnectors, func(item *config.CacheConnectorConfig, index int) (CacheConnector, bool) {
		switch item.Driver {
		case config.Memory:
			return NewInMemoryConnector(item.Id, item.Memory), true
		default:
			return nil, false
		}
	})
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
	}
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
