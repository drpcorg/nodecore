package flow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
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
	upstreamSupervisor upstreams.UpstreamSupervisor
}

func NewUnaryRequestProcessor(chain chains.Chain, upstreamSupervisor upstreams.UpstreamSupervisor) *UnaryRequestProcessor {
	return &UnaryRequestProcessor{
		chain:              chain,
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

	if specs.IsSubscribeMethod(chains.GetMethodSpecNameByChain(u.chain), request.Method()) {
		err = protocol.ClientError(fmt.Errorf("unable to process a subscription request %s", request.Method()))
		response = &protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
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

	parsedParam := request.ParseParams(ctx)
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

			responseHolder, err := sendUnaryRequest(ctx, upstreamSupervisor.GetUpstream(upstreamId), request, parsedParam)
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

// selectAndSend selects a single upstream via the strategy and sends the request
// to it directly, WITHOUT the failsafe executor (no retry/hedge policies). It is
// the lightweight counterpart to executeUnaryRequest for callers that just need a
// one-shot request to a strategy-chosen upstream - e.g. the local logs source
// fetching eth_getLogs from any upstream at the block's height. Repeated calls
// with the same strategy walk down its rating list (selectedUpstreams dedup).
func selectAndSend(
	ctx context.Context,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	request protocol.RequestHolder,
	strategy UpstreamStrategy,
) (*protocol.ResponseHolderWrapper, error) {
	upstreamId, err := strategy.SelectUpstream(request)
	if err != nil {
		return nil, err
	}
	upstream := upstreamSupervisor.GetUpstream(upstreamId)
	if upstream == nil {
		return nil, protocol.NoAvailableUpstreamsError()
	}
	return sendUnaryRequest(ctx, upstream, request, request.ParseParams(ctx))
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
	parsedParam specs.MethodParam,
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

	finalizationBlockType, finalizationBlock := responseFinalizationMetadata(upstreamState, requestBlockTagMetadata(parsedParam))
	if lowerBound, ok := liveLowerBoundFromPrunedError(request.Method(), parsedParam, response, upstream.GetCurrentHeadHeight()); ok {
		upstream.UpdateLowerBound(lowerBound)
	}
	return &protocol.ResponseHolderWrapper{
		RequestId:             request.Id(),
		UpstreamId:            upstream.GetId(),
		UpstreamNodeVersion:   upstreamNodeVersion,
		FinalizationBlockType: finalizationBlockType,
		FinalizationBlock:     finalizationBlock,
		Response:              response,
	}, nil
}

func requestBlockTagMetadata(param specs.MethodParam) *protocol.RequestBlockTag {
	blockNumber, ok := param.(*specs.BlockNumberParam)
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

func liveLowerBoundFromPrunedError(method string, param specs.MethodParam, response protocol.ResponseHolder, currentHead uint64) (protocol.LowerBoundData, bool) {
	if response == nil || !response.HasError() {
		return protocol.LowerBoundData{}, false
	}
	err := response.GetError()
	if err == nil || !isPrunedHistoryError(err.Message) {
		return protocol.LowerBoundData{}, false
	}
	boundType, ok := lowerBoundTypeForMethod(method)
	if !ok {
		return protocol.LowerBoundData{}, false
	}
	block, ok := lowerBoundRequestBlock(param)
	if !ok || block < 0 || currentHead == 0 || uint64(block) > currentHead {
		return protocol.LowerBoundData{}, false
	}
	return protocol.NewLowerBoundDataNow(block+1, boundType), true
}

func lowerBoundTypeForMethod(method string) (protocol.LowerBoundType, bool) {
	if method == "eth_getProof" {
		return protocol.ProofBound, true
	}
	if method == "eth_getLogs" {
		return protocol.LogsBound, true
	}
	if strings.HasPrefix(method, "trace_") || strings.HasPrefix(method, "debug_trace") {
		return protocol.TraceBound, true
	}
	return protocol.UnknownBound, false
}

func lowerBoundRequestBlock(param specs.MethodParam) (int64, bool) {
	switch param := param.(type) {
	case *specs.BlockNumberParam:
		if param.BlockNumber >= 0 {
			return int64(param.BlockNumber), true
		}
	case *specs.BlockRangeParam:
		if param.From != nil && *param.From >= 0 {
			return int64(*param.From), true
		}
	}
	return 0, false
}

func isPrunedHistoryError(message string) bool {
	message = strings.ToLower(message)
	prunedMarkers := []string{
		"missing trie node",
		"missing trie node",
		"state is not available",
		"required historical state unavailable",
		"header not found",
		"history has been pruned",
		"block #", // trace clients report pruned trace history as "block #<n> not found"
		"pruned",
	}
	for _, marker := range prunedMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
