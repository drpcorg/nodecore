package caches

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
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
	None
)

type CachePolicy struct {
	connector          CacheConnector
	methods            mapset.Set[string]
	chains             mapset.Set[chains.Chain]
	blockchainTypes    mapset.Set[chains.BlockchainType]
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
		blockchainTypes:    getCacheBlockchainTypes(policyConfig.BlockchainType),
		methods:            getCacheMethods(policyConfig.Method),
		maxSizeBytes:       int(maxSizeInBytes(policyConfig.ObjectMaxSize)),
		finalizationType:   mapFinalizationType(policyConfig.FinalizationType),
	}
}

func (c *CachePolicy) Store(
	ctx context.Context,
	chain chains.Chain,
	request protocol.RequestHolder,
	response []byte,
) bool {
	if !c.baseCacheableCheck(ctx, chain, request) {
		return false
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
	if err := c.connector.Store(ctx, getCacheKey(chain, request.RequestHash()), string(response), c.ttl); err != nil {
		log.Error().Err(err).Msgf("connector %s of policy %s couldn't cache request %s", c.connector.Id(), c.id, request.Method())
		return false
	}
	return true
}

func (c *CachePolicy) Receive(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) ([]byte, bool) {
	localLog := zerolog.Ctx(ctx)
	if !c.baseCacheableCheck(ctx, chain, request) {
		return nil, false
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
	if chainConfig == "*" || chainConfig == "" {
		// empty set means that any chain can be processed
		// (an empty config happens for blockchain-type-based policies)
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

func getCacheBlockchainTypes(blockchainTypeConfig string) mapset.Set[chains.BlockchainType] {
	if blockchainTypeConfig == "*" || blockchainTypeConfig == "" {
		// empty set means that any blockchain type can be processed
		// (an empty config happens for chain-based policies)
		return mapset.NewThreadUnsafeSet[chains.BlockchainType]()
	}

	cacheBlockchainTypes := lo.Map(strings.Split(blockchainTypeConfig, "|"), func(item string, index int) string {
		return strings.TrimSpace(item)
	})
	cacheBlockchainTypesSet := mapset.NewThreadUnsafeSet[chains.BlockchainType]()
	for _, blockchainTypeStr := range cacheBlockchainTypes {
		if chains.IsValidBlockchainType(blockchainTypeStr) {
			cacheBlockchainTypesSet.Add(chains.BlockchainType(blockchainTypeStr))
		}
	}

	if cacheBlockchainTypesSet.IsEmpty() {
		// nil means that there are no supported blockchain types in the config, so nothing can be processed
		return nil
	}
	return cacheBlockchainTypesSet
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

func (c *CachePolicy) chainNotMatched(chain chains.Chain) bool {
	return c.chains == nil || (!c.chains.IsEmpty() && !c.chains.ContainsOne(chain))
}

func (c *CachePolicy) blockchainTypeNotMatched(chain chains.Chain) bool {
	if c.blockchainTypes == nil {
		return true
	}
	if c.blockchainTypes.IsEmpty() {
		return false
	}
	blockchainType := chains.GetChain(chain.String()).Type
	return !c.blockchainTypes.ContainsOne(blockchainType)
}

func getCacheKey(chain chains.Chain, requestHash string) string {
	return fmt.Sprintf("%s_%s", chain, requestHash)
}

func (c *CachePolicy) isMethodCacheable(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) bool {
	chainsSupervisor := c.upstreamSupervisor.GetChainSupervisor(chain)
	if chainsSupervisor == nil {
		return false
	}

	method := request.SpecMethod()
	if method == nil {
		return false // not cache if no method
	}

	if !method.IsCacheable() {
		return false // according to the method setting check if it's cacheable or not
	}
	methodParam := request.ParseParams(ctx)
	switch param := methodParam.(type) {
	case *specs.BlockNumberParam:
		if specs.IsBlockTagNumber(param.BlockNumber) {
			return false // not cache requests with block tags
		}
		if c.finalizationType == Finalized {
			chainFinalizedBlock, ok := chainsSupervisor.GetChainState().Blocks[protocol.FinalizedBlock]
			if !ok || chainFinalizedBlock.IsEmptyByHeight() {
				return false // don't cache when finalized height is unknown
			}
			if uint64(param.BlockNumber.Int64()) > chainFinalizedBlock.Height {
				return false
			}
		}
	case *specs.BlockRangeParam:
		if param.From != nil && specs.IsBlockTagNumber(*param.From) {
			return false // not cache requests with block tags
		}
		if param.To != nil && specs.IsBlockTagNumber(*param.To) {
			return false // not cache requests with block tags
		}
		if c.finalizationType == Finalized {
			chainFinalizedBlock, ok := chainsSupervisor.GetChainState().Blocks[protocol.FinalizedBlock]
			if !ok || chainFinalizedBlock.IsEmptyByHeight() {
				return false // don't cache when finalized height is unknown
			}
			var maxBlock int64
			if param.From != nil && param.To != nil {
				maxBlock = lo.Max([]int64{param.From.Int64(), param.To.Int64()})
			} else if param.To != nil {
				maxBlock = param.To.Int64()
			} else {
				// to is nil, but from not, means to is latest, so its a block tag
				return false
			}
			if uint64(maxBlock) > chainFinalizedBlock.Height {
				return false
			}
		}
	}

	return true
}

func (c *CachePolicy) baseCacheableCheck(ctx context.Context, chain chains.Chain, request protocol.RequestHolder) bool {
	if !c.isMethodCacheable(ctx, chain, request) { // check spec config if a requested method is cacheable or not
		return false
	}
	if c.chainNotMatched(chain) { // check policy and request chains
		return false
	}
	if c.blockchainTypeNotMatched(chain) { // check policy and request blockchain types
		return false
	}
	if !c.methods.IsEmpty() { // check policy and request methods
		matched := c.methodMatched(request)
		if !matched {
			return false
		}
	}
	return true
}
