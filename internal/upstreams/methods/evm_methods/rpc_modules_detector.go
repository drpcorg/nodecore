package evm_methods

import (
	"context"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// RpcModulesDetector asks the node which JSON-RPC modules it exposes and strips every
// base method whose module is missing. rpc_modules is a reliable negative and an
// unreliable positive: a module the node does not list cannot contain any method, while
// a module it does list may still not implement a given one - which is what
// MethodProbeDetector is for.
type RpcModulesDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
	base            mapset.Set[string]
}

func NewRpcModulesDetector(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
	base mapset.Set[string],
) *RpcModulesDetector {
	return &RpcModulesDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
		base:            base,
	}
}

func (r *RpcModulesDetector) DetectUnsupported(ctx context.Context) mapset.Set[string] {
	unsupported := mapset.NewThreadUnsafeSet[string]()

	modules := r.detectModules(ctx)
	if len(modules) == 0 {
		return unsupported
	}

	for method := range r.base.Iter() {
		module, ok := moduleOf(method)
		if !ok {
			continue
		}
		if _, enabled := modules[module]; !enabled {
			unsupported.Add(method)
		}
	}

	return unsupported
}

// detectModules returns the node's module map, or nil when there is no usable answer.
// Every failure - a node that never implemented rpc_modules, a transient error, a body
// that is not a module map - is nil, i.e. "no opinion", never "nothing is supported".
func (r *RpcModulesDetector) detectModules(ctx context.Context) map[string]string {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("rpc_modules", nil, r.chain)
	if err != nil {
		log.Error().Err(err).Msgf("couldn't create an rpc_modules request of '%s'", r.upstreamId)
		return nil
	}

	requestCtx, cancel := context.WithTimeout(ctx, r.internalTimeout)
	defer cancel()

	response := r.connector.SendRequest(requestCtx, request)
	if response.HasError() {
		log.Warn().Err(response.GetError()).Msgf("couldn't detect rpc_modules of '%s', no methods will be stripped by module", r.upstreamId)
		return nil
	}

	var modules map[string]string
	if err := sonic.Unmarshal(response.ResponseResult(), &modules); err != nil {
		log.Warn().Err(err).Msgf("couldn't parse rpc_modules of '%s', no methods will be stripped by module", r.upstreamId)
		return nil
	}

	return modules
}

// moduleOf returns a method's JSON-RPC module - the part before the first underscore -
// and false when the name has none, in which case no module-level claim can be made
// about it.
func moduleOf(method string) (string, bool) {
	index := strings.Index(method, "_")
	if index <= 0 {
		return "", false
	}
	return method[:index], true
}

var _ methods.MethodsDetector = (*RpcModulesDetector)(nil)
