package protocol

import (
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
)

type RequestKind int

const (
	UnknownReqKind RequestKind = iota
	InternalUnary
	InternalSubscription
	Local
	Cached
	Unary
	Subscription
)

func (r RequestKind) String() string {
	switch r {
	case UnknownReqKind:
		return "unknown"
	case InternalUnary:
		return "internal_unary"
	case InternalSubscription:
		return "internal_subscription"
	case Local:
		return "local"
	case Unary:
		return "unary"
	case Subscription:
		return "subscription"
	case Cached:
		return "cached"
	}
	panic(fmt.Sprintf("unknown request kind %d", r))
}

type ResponseKind int

const (
	UnknownRespKind ResponseKind = iota
	Ok
	Cancelled
	Error
	RetryableError
	RoutingError
)

func (r ResponseKind) String() string {
	switch r {
	case UnknownRespKind:
		return "unknown"
	case Ok:
		return "ok"
	case Cancelled:
		return "cancelled"
	case Error:
		return "error"
	case RetryableError:
		return "retryable_error"
	case RoutingError:
		return "routing_error"
	}
	panic(fmt.Sprintf("unknown response kind %d", r))
}

type RequestObserver struct {
	chain   chains.Chain
	method  string
	reqKind RequestKind
	apiKey  string
	reqCtx  requestCtx
}

func (b *RequestObserver) TrackUpstreamCall() func() {
	return b.reqCtx.trackUpstreamCall()
}

func (b *RequestObserver) AddResult(result RequestResult, final bool) {
	result.
		withChain(b.chain).
		withMethod(b.method).
		withApiKey(b.apiKey).
		withTimestamp(time.Now().UTC()).
		withReqKind(b.reqKind)

	if final {
		b.reqCtx.addFinalResult(result)
	} else {
		b.reqCtx.addResult(result)
	}
}

func (b *RequestObserver) GetChain() chains.Chain {
	return b.chain
}

func (b *RequestObserver) GetRequestKind() RequestKind {
	return b.reqKind
}

func (b *RequestObserver) GetResults() []RequestResult {
	return b.reqCtx.getResults()
}

func (b *RequestObserver) WithChain(chain chains.Chain) *RequestObserver {
	b.chain = chain
	return b
}

func (b *RequestObserver) WithMethod(method string) *RequestObserver {
	b.method = method
	return b
}

func (b *RequestObserver) WithRequestKind(kind RequestKind) *RequestObserver {
	b.reqKind = kind
	return b
}

func (b *RequestObserver) WithApiKey(apiKey string) *RequestObserver {
	b.apiKey = apiKey
	return b
}

func NewRequestObserver(isSub bool) *RequestObserver {
	var reqCtx requestCtx
	if isSub {
		reqCtx = newSubscriptionRequestCtx()
	} else {
		reqCtx = newUnaryRequestCtx()
	}

	return &RequestObserver{
		reqCtx: reqCtx,
	}
}

func GetRespKindFromResponse(response ResponseHolder) ResponseKind {
	var respKind ResponseKind
	switch r := response.(type) {
	case *BaseUpstreamResponse:
		if !r.HasError() {
			respKind = Ok
		} else {
			respKind = lo.Ternary(IsRetryable(response), RetryableError, Error)
		}
	case *ReplyError:
		respKind = Error
		if r.HasError() {
			switch r.GetError().Code {
			case CtxErrorCode:
				respKind = Cancelled
			case NoAvailableUpstreams, NoSupportedMethod:
				respKind = RoutingError
			default:
				respKind = lo.Ternary(IsRetryable(response), RetryableError, Error)
			}

		}
	}
	return respKind
}
