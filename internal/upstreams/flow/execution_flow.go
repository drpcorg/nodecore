package flow

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

var requestTotalMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "request",
		Name:      "requests_total",
		Help:      "Total number of RPC requests sent across all upstreams",
	},
	[]string{"chain", "method"},
)

var requestErrorsMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "request",
		Name:      "errors_total",
		Help:      "The total number of RPC request errors returned by all upstreams",
	},
	[]string{"chain", "method"},
)

var quorumVerificationsMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "quorum",
		Name:      "verifications_total",
		Help:      "QR signature verification outcomes for client-requested quorum reads",
	},
	[]string{"chain", "method", "status", "reason"},
)

func init() {
	prometheus.MustRegister(requestTotalMetric, requestErrorsMetric, quorumVerificationsMetric)
}

// quorumVerifyReason maps a verification error to a stable label value.
// Typed errors from the quorum package are matched first; sentinel fallbacks
// cover MissingSignatures (no typed variant) and the catch-all case.
func quorumVerifyReason(err error) string {
	if err == nil {
		return "ok"
	}
	var notSupported *quorum.NotSupportedError
	if errors.As(err, &notSupported) {
		return "not_supported"
	}
	var unknown *quorum.UnknownProviderError
	if errors.As(err, &unknown) {
		return "unknown_provider"
	}
	var invalid *quorum.InvalidSignatureError
	if errors.As(err, &invalid) {
		return "invalid_signature"
	}
	var insufficient *quorum.InsufficientSignaturesError
	if errors.As(err, &insufficient) {
		return "insufficient_signatures"
	}
	var malformed *quorum.MalformedHeaderError
	if errors.As(err, &malformed) {
		return "malformed_header"
	}
	var unexpected *quorum.UnexpectedRequestIDError
	if errors.As(err, &unexpected) {
		return "request_id_mismatch"
	}
	if errors.Is(err, quorum.ErrMissingSignatures) {
		return "missing_signatures"
	}
	return "other"
}

type ExecutionFlow interface {
	Execute(ctx context.Context, requests []protocol.RequestHolder)
	GetResponses() chan *protocol.ResponseHolderWrapper
	AddHooks(hooks ...any)
}

type BaseExecutionFlow struct {
	chain              chains.Chain
	upstreamSupervisor upstreams.UpstreamSupervisor
	wg                 sync.WaitGroup
	responseChan       chan *protocol.ResponseHolderWrapper
	cacheProcessor     caches.CacheProcessor
	subCtx             *SubCtx
	registry           *rating.RatingRegistry
	appConfig          *config.AppConfig
	quorumRegistry     *quorum.Registry

	hooks struct {
		receivedHooks []protocol.ResponseReceivedHook
	}
}

func NewBaseExecutionFlow(
	chain chains.Chain,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheProcessor caches.CacheProcessor,
	registry *rating.RatingRegistry,
	appConfig *config.AppConfig,
	subCtx *SubCtx,
	quorumRegistry *quorum.Registry,
) *BaseExecutionFlow {
	return &BaseExecutionFlow{
		chain:              chain,
		cacheProcessor:     cacheProcessor,
		upstreamSupervisor: upstreamSupervisor,
		responseChan:       make(chan *protocol.ResponseHolderWrapper),
		subCtx:             subCtx,
		registry:           registry,
		appConfig:          appConfig,
		quorumRegistry:     quorumRegistry,
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

func (e *BaseExecutionFlow) AddHooks(hooks ...any) {
	for _, hook := range hooks {
		if receiveHook, ok := hook.(protocol.ResponseReceivedHook); ok {
			e.hooks.receivedHooks = append(e.hooks.receivedHooks, receiveHook)
		}
	}
}

func (e *BaseExecutionFlow) createStrategy(ctx context.Context, request protocol.RequestHolder) UpstreamStrategy {
	chainSupervisor := e.upstreamSupervisor.GetChainSupervisor(e.chain)
	if request.IsSubscribe() {
		// TODO: calculate rating of subscription methods
		return NewBaseStrategy(chainSupervisor)
	}
	_, quorumRequested := quorum.FromContext(ctx)
	stickySend := specs.IsStickySendMethod(request.SpecMethod())

	// Quorum reads cannot piggyback on sticky-send methods: signatures are
	// computed over a read result, not on a submission, and the sticky
	// matcher would also be dropped by the DRPC-only strategy.
	if quorumRequested && stickySend {
		return NewFailingStrategy(protocol.QuorumNotSupportedError("sticky-send methods"))
	}

	additionalMatchers := make([]Matcher, 0)
	if stickySend {
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
	// Quorum requests may only be served by drpc upstreams via an HTTP-capable
	// connector, since only they return QR signature headers we can verify.
	if quorumRequested {
		sorted := e.registry.GetSortedUpstreams(e.chain, request.Method())
		drpcIds := filterQuorumCapableUpstreams(sorted, e.upstreamSupervisor, request.RequestType())
		if len(drpcIds) == 0 {
			return NewFailingStrategy(protocol.QuorumNotSupportedError("no DRPC upstream with an HTTP connector available for this chain"))
		}
		return NewSpecificOrderUpstreamStrategy(drpcIds, chainSupervisor)
	}
	return NewRatingStrategy(e.chain, request.Method(), additionalMatchers, chainSupervisor, e.registry)
}

// filterQuorumCapableUpstreams keeps only DRPC upstreams that expose a
// connector capable of serving the given request type over HTTP (QR headers
// come only via HTTP responses — WS/gRPC cannot carry them).
func filterQuorumCapableUpstreams(
	upstreamIds []string,
	supervisor upstreams.UpstreamSupervisor,
	requestType protocol.RequestType,
) []string {
	out := make([]string, 0, len(upstreamIds))
	for _, id := range upstreamIds {
		up := supervisor.GetUpstream(id)
		if up == nil {
			continue
		}
		if up.GetVendorType() != upstreams.DRPC {
			continue
		}
		if !hasHttpConnector(up, requestType) {
			continue
		}
		out = append(out, id)
	}
	return out
}

func hasHttpConnector(up upstreams.Upstream, requestType protocol.RequestType) bool {
	switch requestType {
	case protocol.Rest:
		return up.GetConnector(protocol.RestConnector) != nil
	default:
		return up.GetConnector(protocol.JsonRpcConnector) != nil
	}
}

func (e *BaseExecutionFlow) processRequest(ctx context.Context, upstreamStrategy UpstreamStrategy, request protocol.RequestHolder) {
	go func() {
		defer e.wg.Done()
		requestTotalMetric.WithLabelValues(e.chain.String(), request.Method()).Inc()

		reqObserver := request.RequestObserver().WithChain(e.chain)

		execCtx := context.WithValue(ctx, resilience.RequestKey, request)
		requestProcessor := e.createRequestProcessor(request)

		now := time.Now()
		processedResponse := requestProcessor.ProcessRequest(execCtx, upstreamStrategy, request)
		duration := time.Since(now).Seconds()

		switch resp := processedResponse.(type) {
		case *UnaryResponse:
			e.verifyQuorumSignatures(ctx, request, resp.ResponseWrapper)

			if protocol.IsRetryable(resp.ResponseWrapper.Response) {
				requestErrorsMetric.WithLabelValues(e.chain.String(), request.Method()).Inc()
			}

			reqObserver.AddResult(
				protocol.NewUnaryRequestResult().
					WithDuration(duration).
					WithUpstreamId(resp.ResponseWrapper.UpstreamId).
					WithRespKindFromResponse(resp.ResponseWrapper.Response),
				true,
			)

			e.responseReceive(ctx, request, resp.ResponseWrapper)
			e.sendResponse(ctx, resp.ResponseWrapper, request)
		case *SubscriptionResponse:
			for wrapper := range resp.ResponseWrappers {
				e.sendResponse(ctx, wrapper, request)
			}
		}
	}()
}

func (e *BaseExecutionFlow) createRequestProcessor(request protocol.RequestHolder) RequestProcessor {
	reqObserver := request.RequestObserver()
	var requestProcessor RequestProcessor

	if request.IsSubscribe() {
		requestProcessor = NewSubscriptionRequestProcessor(e.upstreamSupervisor, e.subCtx)
	} else if isLocalRequest(e.chain, request.Method()) {
		requestProcessor = NewLocalRequestProcessor(e.chain, e.subCtx)
		reqObserver.WithRequestKind(protocol.Local)
	} else if isStickyRequest(request.SpecMethod()) {
		requestProcessor = NewStickyRequestProcessor(e.chain, e.upstreamSupervisor)
		reqObserver.WithRequestKind(protocol.Unary)
	} else if shouldEnforceIntegrity(request.SpecMethod(), e.appConfig.UpstreamConfig.IntegrityConfig) {
		requestProcessor = NewIntegrityRequestProcessor(
			e.chain,
			e.upstreamSupervisor,
			NewUnaryRequestProcessor(e.chain, e.cacheProcessor, e.upstreamSupervisor),
		)
		reqObserver.WithRequestKind(protocol.Unary)
	} else {
		requestProcessor = NewUnaryRequestProcessor(e.chain, e.cacheProcessor, e.upstreamSupervisor)
		reqObserver.WithRequestKind(protocol.Unary)
	}

	return requestProcessor
}

func (e *BaseExecutionFlow) sendResponse(ctx context.Context, wrapper *protocol.ResponseHolderWrapper, request protocol.RequestHolder) {
	select {
	case <-ctx.Done():
		zerolog.Ctx(ctx).Trace().Msgf("request %s has been cancelled, dropping the response", request.Method())
	case e.responseChan <- wrapper:
	}
}

func (e *BaseExecutionFlow) responseReceive(ctx context.Context, request protocol.RequestHolder, responseWrapper *protocol.ResponseHolderWrapper) {
	for _, hook := range e.hooks.receivedHooks {
		hook.OnResponseReceived(ctx, request, responseWrapper)
	}
}

// verifyQuorumSignatures runs the QR-header signature verifier when the
// incoming client request asked for quorum reads. On failure the response
// wrapper is replaced with a protocol-level error so the client sees why the
// quorum attestation was rejected instead of trusting the body.
//
// An upstream error response is passed through untouched — the client sees
// the upstream's own error, not a fake "quorum failed". Any other shape
// that cannot be verified (stream / response without headers) is rejected
// with QuorumNotSupported: streaming was supposed to be force-disabled by
// HttpConnector, so reaching either branch indicates a misconfigured path.
func (e *BaseExecutionFlow) verifyQuorumSignatures(
	ctx context.Context,
	request protocol.RequestHolder,
	wrapper *protocol.ResponseHolderWrapper,
) {
	if e.quorumRegistry == nil || wrapper == nil || wrapper.Response == nil {
		return
	}
	params, ok := quorum.FromContext(ctx)
	if !ok {
		return
	}
	if wrapper.Response.HasError() {
		return
	}

	var verifyErr error
	switch {
	case wrapper.Response.HasStream():
		verifyErr = &quorum.NotSupportedError{Reason: "upstream returned a stream; quorum requires a buffered body"}
	default:
		headerBearer, ok := wrapper.Response.(protocol.HasResponseHeaders)
		if !ok {
			verifyErr = &quorum.NotSupportedError{Reason: "upstream response does not carry HTTP headers"}
			break
		}
		// Upstreams echo back the client-facing JSON-RPC id (sent on the wire
		// via request.Body()), not nodecore's internal UUID tag. Fall back to
		// Id() only if the request does not expose a real id accessor.
		var expectedReqID string
		if rr, ok := request.(interface{ RealId() string }); ok {
			expectedReqID = rr.RealId()
		} else {
			expectedReqID = request.Id()
		}
		verifyErr = e.quorumRegistry.VerifyHeaders(
			headerBearer.ResponseHeaders(),
			wrapper.Response.ResponseResult(),
			expectedReqID,
			params.Quorum,
		)
	}
	if verifyErr == nil {
		quorumVerificationsMetric.WithLabelValues(
			e.chain.String(), request.Method(), "ok", "ok",
		).Inc()
		return
	}

	reason := quorumVerifyReason(verifyErr)
	quorumVerificationsMetric.WithLabelValues(
		e.chain.String(), request.Method(), "fail", reason,
	).Inc()

	zerolog.Ctx(ctx).Warn().
		Err(verifyErr).
		Str("upstream", wrapper.UpstreamId).
		Str("method", request.Method()).
		Str("reason", reason).
		Msg("quorum signature verification failed")

	wrapper.Response = protocol.NewTotalFailureFromErr(
		wrapper.RequestId,
		quorum.ToResponseError(verifyErr),
		request.RequestType(),
	)
}

func isLocalRequest(chain chains.Chain, method string) bool {
	return specs.IsLocalMethod(chains.GetMethodSpecNameByChain(chain), method)
}

func isStickyRequest(specMethod *specs.Method) bool {
	return specs.IsStickyCreateMethod(specMethod) || specs.IsStickySendMethod(specMethod)
}

func shouldEnforceIntegrity(specMethod *specs.Method, integrityConfig *config.IntegrityConfig) bool {
	return integrityConfig.Enabled && specMethod != nil && specMethod.ShouldEnforceIntegrity()
}

type SubCtx struct {
	subscriptions      *utils.CMap[string, context.CancelFunc]
	subscriptionResult bool
}

func NewSubCtx() *SubCtx {
	return &SubCtx{
		subscriptions: utils.NewCMap[string, context.CancelFunc](),
	}
}

func (s *SubCtx) WithSubscriptionResultOnly(enabled bool) *SubCtx {
	s.subscriptionResult = enabled
	return s
}

func (s *SubCtx) IsSubscriptionResultOnly() bool {
	return s.subscriptionResult
}

func (s *SubCtx) AddSub(sub string, cancel context.CancelFunc) {
	s.subscriptions.Store(sub, cancel)
}

func (s *SubCtx) Unsubscribe(sub string) {
	cancel, ok := s.subscriptions.Load(sub)
	if ok {
		s.subscriptions.Delete(sub)
		cancel()
	}
}

func (s *SubCtx) Exists(sub string) bool {
	_, ok := s.subscriptions.Load(sub)
	return ok
}
