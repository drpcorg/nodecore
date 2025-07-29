package flow

import (
	"context"
	"github.com/drpcorg/dsheltie/internal/caches"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/rating"
	"github.com/drpcorg/dsheltie/internal/resilience"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"sync"
)

var requestTotalMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Name:      "requests_total",
	},
	[]string{"chain", "method"},
)

var requestErrorsMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Name:      "errors_total",
	},
	[]string{"chain", "method"},
)

func init() {
	prometheus.MustRegister(requestTotalMetric, requestErrorsMetric)
}

type ExecutionFlow interface {
	Execute(ctx context.Context, requests []protocol.RequestHolder)
	GetResponses() chan *protocol.ResponseHolderWrapper
}

type BaseExecutionFlow struct {
	chain              chains.Chain
	upstreamSupervisor upstreams.UpstreamSupervisor
	wg                 sync.WaitGroup
	responseChan       chan *protocol.ResponseHolderWrapper
	cacheProcessor     caches.CacheProcessor
	subCtx             *SubCtx
	registry           *rating.RatingRegistry
}

func NewBaseExecutionFlow(
	chain chains.Chain,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheProcessor caches.CacheProcessor,
	registry *rating.RatingRegistry,
	subCtx *SubCtx,
) *BaseExecutionFlow {
	return &BaseExecutionFlow{
		chain:              chain,
		cacheProcessor:     cacheProcessor,
		upstreamSupervisor: upstreamSupervisor,
		responseChan:       make(chan *protocol.ResponseHolderWrapper),
		subCtx:             subCtx,
		registry:           registry,
	}
}

func (e *BaseExecutionFlow) GetResponses() chan *protocol.ResponseHolderWrapper {
	return e.responseChan
}

func (e *BaseExecutionFlow) Execute(ctx context.Context, requests []protocol.RequestHolder) {
	defer close(e.responseChan)
	e.wg.Add(len(requests))

	for _, request := range requests {
		e.processRequest(ctx, e.createStrategy(ctx, request), request)
	}

	e.wg.Wait()
}

func (e *BaseExecutionFlow) createStrategy(ctx context.Context, request protocol.RequestHolder) UpstreamStrategy {
	chainSupervisor := e.upstreamSupervisor.GetChainSupervisor(e.chain)
	if request.IsSubscribe() {
		// TODO: calculate rating of subscription methods
		return NewBaseStrategy(chainSupervisor)
	}
	additionalMatchers := make([]Matcher, 0)
	if specs.IsStickySendMethod(request.SpecMethod()) {
		upstreamIndex := ""
		methodParam := request.ParseParams(ctx)
		switch param := methodParam.(type) {
		case *specs.StringParam:
			if len(param.Value) > maxBytes {
				upstreamIndex = param.Value[len(param.Value)-maxBytes:]
			}
		}
		additionalMatchers = append(additionalMatchers, NewUpstreamIndexMatcher(upstreamIndex))
	}
	return NewRatingStrategy(e.chain, request.Method(), additionalMatchers, chainSupervisor, e.registry)
}

func (e *BaseExecutionFlow) processRequest(ctx context.Context, upstreamStrategy UpstreamStrategy, request protocol.RequestHolder) {
	go func() {
		requestTotalMetric.WithLabelValues(e.chain.String(), request.Method()).Inc()

		defer e.wg.Done()
		execCtx := context.WithValue(ctx, resilience.RequestKey, request)
		var requestProcessor RequestProcessor

		if request.IsSubscribe() {
			requestProcessor = NewSubscriptionRequestProcessor(e.upstreamSupervisor, e.subCtx)
		} else if isLocalRequest(e.chain, request.Method()) {
			requestProcessor = NewLocalRequestProcessor(e.chain, e.subCtx)
		} else if isStickyRequest(request.SpecMethod()) {
			requestProcessor = NewStickyRequestProcessor(e.chain, e.upstreamSupervisor)
		} else {
			requestProcessor = NewUnaryRequestProcessor(e.chain, e.cacheProcessor, e.upstreamSupervisor)
		}

		processedResponse := requestProcessor.ProcessRequest(execCtx, upstreamStrategy, request)

		switch resp := processedResponse.(type) {
		case *UnaryResponse:
			if protocol.IsRetryable(resp.ResponseWrapper.Response) {
				requestErrorsMetric.WithLabelValues(e.chain.String(), request.Method()).Inc()
			}
			e.sendResponse(ctx, resp.ResponseWrapper, request)
		case *SubscriptionResponse:
			for wrapper := range resp.ResponseWrappers {
				e.sendResponse(ctx, wrapper, request)
			}
		}
	}()
}

func (e *BaseExecutionFlow) sendResponse(ctx context.Context, wrapper *protocol.ResponseHolderWrapper, request protocol.RequestHolder) {
	select {
	case <-ctx.Done():
		zerolog.Ctx(ctx).Trace().Msgf("request %s has been cancelled, dropping the response", request.Method())
	case e.responseChan <- wrapper:
	}
}

func isLocalRequest(chain chains.Chain, method string) bool {
	return specs.IsUnsubscribeMethod(chains.GetMethodSpecNameByChain(chain), method)
}

func isStickyRequest(specMethod *specs.Method) bool {
	return specs.IsStickyCreateMethod(specMethod) || specs.IsStickySendMethod(specMethod)
}

type SubCtx struct {
	subscriptions *utils.CMap[string, context.CancelFunc]
}

func NewSubCtx() *SubCtx {
	return &SubCtx{
		subscriptions: utils.NewCMap[string, context.CancelFunc](),
	}
}

func (s *SubCtx) AddSub(sub string, cancel context.CancelFunc) {
	s.subscriptions.Store(sub, &cancel)
}

func (s *SubCtx) Unsubscribe(sub string) {
	cancel, ok := s.subscriptions.Load(sub)
	if ok {
		s.subscriptions.Delete(sub)
		(*cancel)()
	}
}

func (s *SubCtx) Exists(sub string) bool {
	_, ok := s.subscriptions.Load(sub)
	return ok
}
