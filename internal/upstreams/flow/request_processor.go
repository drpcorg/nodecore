package flow

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/failsafe-go/failsafe-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

var hedgeMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "request",
		Name:      "hedge_hit",
		Help:      "The total number of hedged RPC requests executed on an upstream",
	},
	[]string{"chain", "method", "upstream"},
)

func init() {
	prometheus.MustRegister(hedgeMetric)
}

type ProcessedResponse interface {
	response()
}

type UnaryResponse struct {
	ResponseWrapper *protocol.ResponseHolderWrapper
}

func (u *UnaryResponse) response() {
}

var _ ProcessedResponse = (*UnaryResponse)(nil)

type SubscriptionResponse struct {
	ResponseWrappers chan *protocol.ResponseHolderWrapper
}

func (s *SubscriptionResponse) response() {
}

var _ ProcessedResponse = (*SubscriptionResponse)(nil)

type RequestProcessor interface {
	ProcessRequest(ctx context.Context, upstreamStrategy UpstreamStrategy, request protocol.RequestHolder) ProcessedResponse
}

type UnaryRequestProcessor struct {
	chain              chains.Chain
	cacheProcessor     caches.CacheProcessor
	upstreamSupervisor upstreams.UpstreamSupervisor
}

func NewUnaryRequestProcessor(chain chains.Chain, cacheProcessor caches.CacheProcessor, upstreamSupervisor upstreams.UpstreamSupervisor) *UnaryRequestProcessor {
	return &UnaryRequestProcessor{
		chain:              chain,
		cacheProcessor:     cacheProcessor,
		upstreamSupervisor: upstreamSupervisor,
	}
}

func (u *UnaryRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	var response *protocol.ResponseHolderWrapper
	var err error
	fromCache := false // don't store responses from cache
	// Quorum-read requests are served fresh from drpc upstreams: cached payloads
	// don't carry the QR* signature headers, so both cache Receive and Store
	// are skipped here.
	_, quorumRequested := quorum.FromContext(ctx)

	if specs.IsSubscribeMethod(chains.GetMethodSpecNameByChain(u.chain), request.Method()) {
		err = protocol.ClientError(fmt.Errorf("unable to process a subscription request %s", request.Method()))
		response = &protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
		}
	} else {
		var result []byte
		var ok bool
		if !quorumRequested {
			result, ok = u.cacheProcessor.Receive(ctx, u.chain, request)
		}
		if ok {
			// change the previous request type since it will not be sent to the upstream
			request.RequestObserver().
				WithRequestKind(protocol.Cached)
			fromCache = true
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewSimpleHttpUpstreamResponse(request.Id(), result, request.RequestType()),
			}
		} else {
			response, err = executeUnaryRequest(ctx, u.chain, request, u.upstreamSupervisor, upstreamStrategy)
			if err != nil {
				response = &protocol.ResponseHolderWrapper{
					UpstreamId: NoUpstream,
					RequestId:  request.Id(),
					Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
				}
			}
		}
	}

	if !fromCache && !quorumRequested && !response.Response.HasError() && !response.Response.HasStream() {
		go u.cacheProcessor.Store(ctx, u.chain, request, response.Response.ResponseResult())
	}

	return &UnaryResponse{ResponseWrapper: response}
}

func handleErrors(exec failsafe.Execution[*protocol.ResponseHolderWrapper], err error) error {
	if exec.Retries() > 0 {
		return protocol.StopRetryErr{}
	}
	return err
}

func executeUnaryRequest(
	ctx context.Context,
	chain chains.Chain,
	request protocol.RequestHolder,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	upstreamStrategy UpstreamStrategy,
) (*protocol.ResponseHolderWrapper, error) {
	firstUpstream := utils.NewAtomic[string]()
	hedged := atomic.Bool{}

	result, err := upstreamSupervisor.
		GetExecutor().
		WithContext(ctx).
		GetWithExecution(func(exec failsafe.Execution[*protocol.ResponseHolderWrapper]) (*protocol.ResponseHolderWrapper, error) {
			upstreamId, err := upstreamStrategy.SelectUpstream(request)
			if err != nil {
				return nil, handleErrors(exec, err)
			}
			if firstUpstream.Load() == "" {
				firstUpstream.Store(upstreamId)
			}

			responseHolder, err := sendUnaryRequest(ctx, upstreamSupervisor.GetUpstream(upstreamId), request)
			if err != nil {
				return nil, handleErrors(exec, err)
			}
			if exec.Hedges() > 0 {
				hedged.Store(true)
			}
			return responseHolder, nil
		})

	if hedged.Load() {
		// it's important to track the very first upstream that caused the hedge logic
		hedgeMetric.WithLabelValues(chain.String(), request.Method(), firstUpstream.Load()).Inc()
	}

	return result, err
}

func getMethodConnector(upstream upstreams.Upstream, method *specs.Method) connectors.ApiConnector {
	for _, connector := range method.GetApiConnectorTypes() {
		if upConnector := upstream.GetConnector(connector); upConnector != nil {
			return upConnector
		}
	}
	return nil
}

func sendUnaryRequest(
	ctx context.Context,
	upstream upstreams.Upstream,
	request protocol.RequestHolder,
) (*protocol.ResponseHolderWrapper, error) {
	zerolog.Ctx(ctx).Debug().Msgf("sending a request %s to upstream %s", request.Method(), upstream.GetId())

	apiConnector := getMethodConnector(upstream, request.SpecMethod())
	if apiConnector == nil {
		return nil, protocol.NoApiConnectorsError(request.Method())
	}

	response := apiConnector.SendRequest(ctx, request)

	if response.ResponseCode() == http.StatusTooManyRequests && upstream.GetUpstreamState().AutoTuneRateLimiter != nil {
		upstream.GetUpstreamState().AutoTuneRateLimiter.IncErrors()
	}
	upstreamState := upstream.GetUpstreamState()
	upstreamNodeVersion := ""
	if upstreamState.Labels != nil {
		if version, ok := upstreamState.Labels.GetLabel("client_version"); ok {
			upstreamNodeVersion = version
		}
	}

	requestBlockTag := requestBlockTagMetadata(ctx, request)
	finalizationBlockType, finalizationBlock := responseFinalizationMetadata(upstreamState, requestBlockTag)
	return &protocol.ResponseHolderWrapper{
		RequestId:             request.Id(),
		UpstreamId:            upstream.GetId(),
		UpstreamNodeVersion:   upstreamNodeVersion,
		RequestBlockTag:       requestBlockTag,
		FinalizationBlockType: finalizationBlockType,
		FinalizationBlock:     finalizationBlock,
		Response:              response,
	}, nil
}

func requestBlockTagMetadata(ctx context.Context, request protocol.RequestHolder) *protocol.RequestBlockTag {
	if request == nil {
		return nil
	}
	blockNumber, ok := request.ParseParams(ctx).(*specs.BlockNumberParam)
	if !ok {
		return nil
	}

	var tag protocol.RequestBlockTag
	switch blockNumber.BlockNumber {
	case rpc.LatestBlockNumber:
		tag = protocol.BlockTagLatest
	case rpc.SafeBlockNumber:
		tag = protocol.BlockTagSafe
	case rpc.FinalizedBlockNumber:
		tag = protocol.BlockTagFinalized
	default:
		return nil
	}

	return &tag
}

func responseFinalizationMetadata(upstreamState protocol.UpstreamState, tag *protocol.RequestBlockTag) (*protocol.BlockType, protocol.Block) {
	if tag == nil || upstreamState.BlockInfo == nil {
		return nil, protocol.Block{}
	}

	var blockType protocol.BlockType
	switch *tag {
	case protocol.BlockTagSafe:
		blockType = protocol.SafeBlock
	case protocol.BlockTagFinalized:
		blockType = protocol.FinalizedBlock
	default:
		return nil, protocol.Block{}
	}

	block := upstreamState.BlockInfo.GetBlock(blockType)
	if block.IsFullEmpty() {
		return nil, protocol.Block{}
	}
	return &blockType, block
}
