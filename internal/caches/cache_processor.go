package caches

import (
	"context"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"sync"
	"time"
)

type CacheProcessor struct {
	policies       []*CachePolicy
	receiveTimeout time.Duration
}

func NewCacheProcessor(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheConfig *config.CacheConfig,
	receiveTimeout time.Duration,
) *CacheProcessor {
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

	return &CacheProcessor{
		receiveTimeout: receiveTimeout,
		policies:       cachePolicies,
	}
}

func (c *CacheProcessor) Store(
	chain chains.Chain,
	request protocol.RequestHolder,
	response []byte,
) {
	for _, policy := range c.policies {
		policy.Store(chain, request, response)
	}
}

func (c *CacheProcessor) Receive(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) ([]byte, bool) {
	if len(c.policies) == 0 {
		return nil, false
	}

	resultChan := make(chan []byte)
	newCtx, cancel := context.WithTimeout(ctx, c.receiveTimeout)
	defer func() {
		cancel()
		close(resultChan)
	}()

	var once sync.Once

	var wg sync.WaitGroup
	wg.Add(len(c.policies))
	notFoundFunc := func() {
		once.Do(func() {
			resultChan <- nil
		})
	}
	go func() { // wait for all responses if there is nothing in cache
		wg.Wait()
		notFoundFunc()
	}()
	go func() {
		<-newCtx.Done()
		notFoundFunc()
	}()

	for _, policy := range c.policies {
		go func() {
			defer wg.Done()
			result, ok := policy.Receive(newCtx, chain, request)
			if ok {
				once.Do(func() {
					resultChan <- result
					cancel()
				})
			}
		}()
	}

	result := <-resultChan
	return result, len(result) > 0
}
