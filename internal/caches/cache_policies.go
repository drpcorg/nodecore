package caches

import (
	"bytes"
	"context"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"path"
	"strconv"
	"strings"
	"time"
)

var EmptyResponses = [][]byte{
	[]byte(`"0x"`),
	[]byte(`null`),
	[]byte(`{}`),
	[]byte(`[]`),
}

type finalizationType int

const (
	Finalized finalizationType = iota
	Unfinalized
	None
)

type CachePolicy struct {
	connector          CacheConnector
	methods            mapset.Set[string]
	chains             mapset.Set[chains.Chain]
	cacheEmpty         bool
	maxSizeBytes       int
	ttl                time.Duration
	upstreamSupervisor upstreams.UpstreamSupervisor
	id                 string
	finalizationType   finalizationType
}

func NewCachePolicy(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheConnector CacheConnector,
	policyConfig *config.CachePolicyConfig,
) *CachePolicy {
	// TODO: add method config to check if method can be cached or not

	ttl, err := time.ParseDuration(policyConfig.TTL)
	if err != nil {
		ttl = 10 * time.Minute
	}

	return &CachePolicy{
		id:                 policyConfig.Id,
		connector:          cacheConnector,
		upstreamSupervisor: upstreamSupervisor,
		cacheEmpty:         policyConfig.CacheEmpty,
		ttl:                ttl,
		chains:             getCacheChains(policyConfig.Chain),
		methods:            getCacheMethods(policyConfig.Method),
		maxSizeBytes:       int(maxSizeInBytes(policyConfig.ObjectMaxSize)),
		finalizationType:   mapFinalizationType(policyConfig.FinalizationType),
	}
}

func (c *CachePolicy) Store(
	chain chains.Chain,
	request protocol.RequestHolder,
	response []byte,
) bool {
	if c.chainNotMatched(chain) { // check policy and request chains
		return false
	}
	if !c.methods.IsEmpty() { // check policy and request methods
		matched := c.methodMatched(request)
		if !matched {
			return false
		}
	}
	if len(response) > c.maxSizeBytes { // check if a response body doesn't exceed the maximum size of a cacheable item
		return false
	}
	if !c.cacheEmpty { // if empty responses can't be stored, check if a response body is one of the empty responses
		for _, emptyResponse := range EmptyResponses {
			if bytes.Equal(emptyResponse, response) {
				return false
			}
		}
	}
	if c.finalizedStateNotMatched() { // extract a request block number and compare it to the chain finalization state
		return false
	}
	if err := c.connector.Store(context.Background(), getCacheKey(chain, request.RequestHash()), string(response), c.ttl); err != nil {
		log.Warn().Err(err).Msgf("connector %s of policy %s couldn't cache request %s", c.connector.Id(), c.id, request.Method())
		return false
	}
	return true
}

func (c *CachePolicy) Receive(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) ([]byte, bool) {
	localLog := zerolog.Ctx(ctx)
	if c.chainNotMatched(chain) {
		return nil, false
	}
	if !c.methods.IsEmpty() {
		matched := c.methodMatched(request)
		if !matched {
			return nil, false
		}
	}
	cacheKey := getCacheKey(chain, request.RequestHash())

	object, err := c.connector.Receive(ctx, cacheKey)
	if err != nil {
		localLog.
			Debug().
			Err(err).
			Msgf("couldn't receive %s request from the cache connector %s with policy %s", request.Method(), c.connector.Id(), c.id)
		return nil, false
	}
	return object, len(object) > 0
}

func mapFinalizationType(finalizationType config.FinalizationType) finalizationType {
	switch finalizationType {
	case config.Finalized:
		return Finalized
	case config.Unfinalized:
		return Unfinalized
	case config.None:
		return None
	}
	panic(fmt.Sprintf("unknown finalization type - %s", finalizationType))
}

func maxSizeInBytes(maxSizeStr string) int64 {
	multiplier := int64(1024)
	var maxSize string

	if strings.HasSuffix(maxSizeStr, "MB") {
		multiplier = 1024 * 1024
		maxSize = strings.TrimSuffix(maxSizeStr, "MB")
	} else if strings.HasSuffix(maxSizeStr, "KB") {
		multiplier = 1024
		maxSize = strings.TrimSuffix(maxSizeStr, "KB")
	}

	if maxSize == "" {
		return multiplier
	}

	maxSizeInt, err := strconv.ParseInt(strings.TrimSpace(maxSize), 10, 64)
	if err != nil {
		return multiplier
	}

	return maxSizeInt * multiplier
}

func getCacheChains(chainConfig string) mapset.Set[chains.Chain] {
	if chainConfig == "*" {
		// empty set means that any chain can be processed
		return mapset.NewThreadUnsafeSet[chains.Chain]()
	}

	cacheChains := lo.Map(strings.Split(chainConfig, "|"), func(item string, index int) string {
		return strings.TrimSpace(item)
	})
	cacheChainsSet := mapset.NewThreadUnsafeSet[chains.Chain]()
	for _, chainStr := range cacheChains {
		if chains.IsSupported(chainStr) {
			cacheChainsSet.Add(chains.GetChain(chainStr).Chain)
		}
	}

	if cacheChainsSet.IsEmpty() {
		// nil means that there are no supported chains in the config, so no chain can be processed
		return nil
	}
	return cacheChainsSet
}

func getCacheMethods(methodConfig string) mapset.Set[string] {
	if methodConfig == "*" {
		return mapset.NewThreadUnsafeSet[string]()
	}

	cacheMethods := lo.Map(strings.Split(methodConfig, "|"), func(item string, index int) string {
		return strings.TrimSpace(item)
	})
	cacheMethodsSet := mapset.NewThreadUnsafeSet[string]()
	for _, method := range cacheMethods {
		cacheMethodsSet.Add(method)
	}

	return cacheMethodsSet
}

func (c *CachePolicy) methodMatched(request protocol.RequestHolder) bool {
	for _, method := range c.methods.ToSlice() {
		if strings.Contains(method, "*") {
			ok, _ := path.Match(method, request.Method())
			if ok {
				return true
			}
		} else {
			if method == request.Method() {
				return true
			}
		}
	}
	return false
}

func (c *CachePolicy) finalizedStateNotMatched() bool {
	// TODO: use future extractors to get the request block number and compare it to the chain finalization state
	return false
}

func (c *CachePolicy) chainNotMatched(chain chains.Chain) bool {
	return c.chains == nil || (!c.chains.IsEmpty() && !c.chains.ContainsOne(chain))
}

func getCacheKey(chain chains.Chain, requestHash string) string {
	return fmt.Sprintf("%s_%s", chain, requestHash)
}
