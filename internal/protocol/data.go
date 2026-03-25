package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/errors_config"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

type ResponseReceivedHook interface {
	OnResponseReceived(ctx context.Context, request RequestHolder, respWrapper *ResponseHolderWrapper)
}

type SubscribeConnectorState int

const (
	WsDisconnected SubscribeConnectorState = iota
	WsConnected
)

type ResultType int

const (
	ResultOk ResultType = iota
	ResultOkWithError
	ResultPartialFailure
	ResultTotalFailure
	ResultStop
)

type ClientRetryableError struct {
	err error
}

func (e ClientRetryableError) Error() string {
	return e.err.Error()
}

func NewClientRetryableError(err error) *ClientRetryableError {
	return &ClientRetryableError{err: err}
}

type StopRetryErr struct {
}

func (s StopRetryErr) Error() string {
	return "no retry"
}

func GetResponseType(wrapper *ResponseHolderWrapper, err error) ResultType {
	if wrapper != nil {
		switch response := wrapper.Response.(type) {
		case *ReplyError:
			if response.ErrorKind == PartialFailure {
				return ResultPartialFailure
			} else {
				return ResultTotalFailure
			}
		case *BaseUpstreamResponse:
			if response.HasError() {
				return ResultOkWithError
			}
		}
	}
	if err != nil {
		if errors.Is(err, StopRetryErr{}) {
			return ResultStop
		}
		return ResultTotalFailure
	}
	return ResultOk
}

func IsRetryable(response ResponseHolder) bool {
	shouldRetry := false

	switch resp := response.(type) {
	case *BaseUpstreamResponse:
		shouldRetry = response.HasError() && errors_config.IsRetryable(response.GetError().Message)
	case *ReplyError:
		shouldRetry = resp.ErrorKind == PartialFailure
	}

	return shouldRetry
}

func IsStream(method string) bool {
	// TODO: implement logic to determine if a method is streaming or not
	return method == "eth_getLogs" || method == "getProgramAccounts"
}

type ResponseErrorKind int

const (
	PartialFailure ResponseErrorKind = iota
	TotalFailure
)

type BlockType int

const (
	FinalizedBlock BlockType = iota
)

func (b BlockType) String() string {
	switch b {
	case FinalizedBlock:
		return "finalized"
	default:
		panic(fmt.Sprintf("unknown blockType %d", b))
	}
}

type ApiConnectorType int

const (
	JsonRpcConnector ApiConnectorType = iota
	RestConnector
	GrpcConnector
	WsConnector
)

func (a ApiConnectorType) String() string {
	switch a {
	case JsonRpcConnector:
		return "JsonRpc"
	case RestConnector:
		return "REST"
	case GrpcConnector:
		return "GRPC"
	case WsConnector:
		return "WS"
	default:
		panic(fmt.Sprintf("unknown connector type %d", a))
	}
}

type JsonRpcRequestBody struct {
	Id      json.RawMessage `json:"id"`
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func newJsonRpcRequestBody(id json.RawMessage, method string, params json.RawMessage) *JsonRpcRequestBody {
	return &JsonRpcRequestBody{
		Id:      id,
		Method:  method,
		Params:  params,
		Jsonrpc: "2.0",
	}
}

type RequestHolder interface {
	Id() string
	Method() string
	Headers() map[string]string
	Body() ([]byte, error)
	ParseParams(ctx context.Context) specs.MethodParam
	RequestType() RequestType
	RequestHash() string
	SpecMethod() *specs.Method
	RequestObserver() *RequestObserver

	ModifyParams(ctx context.Context, newValue any)

	IsStream() bool
	IsSubscribe() bool
}

type ResponseHolder interface {
	ResponseResult() []byte
	ResponseResultString() (string, error)
	ResponseCode() int
	GetError() *ResponseError
	EncodeResponse(realId []byte) io.Reader
	HasError() bool
	HasStream() bool
	Id() string
}

type SubscriptionResponseHolder interface {
	ResponseHolder
	IsEventFrame() bool
}

type UpstreamSubscriptionResponse interface {
	ResponseChan() chan *WsResponse
}

type ResponseHolderWrapper struct {
	UpstreamId string
	RequestId  string
	Response   ResponseHolder
}

type AvailabilityStatus int

const (
	Available AvailabilityStatus = iota + 1
	Immature
	Syncing
	Unavailable

	UnknownStatus = math.MaxInt
)

func (a AvailabilityStatus) String() string {
	switch a {
	case Available:
		return "AVAILABLE"
	case Unavailable:
		return "UNAVAILABLE"
	case UnknownStatus:
		return "UNKNOWN"
	case Syncing:
		return "SYNCING"
	case Immature:
		return "IMMATURE"
	}
	panic(fmt.Sprintf("unknown status %d", a))
}

type UpstreamEvent struct {
	Id        string
	Chain     chains.Chain
	EventType UpstreamEventType
}

type UpstreamEventType interface {
	eventData()
}

type StateUpstreamEvent struct {
	State *UpstreamState
}

func (u StateUpstreamEvent) eventData() {
}

type RemoveUpstreamEvent struct{}

func (r RemoveUpstreamEvent) eventData() {}

type ValidUpstreamEvent struct{}

func (r ValidUpstreamEvent) eventData() {}

type Cap int

const (
	WsCap Cap = iota
)

type UpstreamState struct {
	Status          AvailabilityStatus
	HeadData        Block
	UpstreamMethods methods.Methods
	Caps            mapset.Set[Cap]
	UpstreamIndex   string

	RateLimiterBudget   *ratelimiter.RateLimitBudget
	AutoTuneRateLimiter *ratelimiter.UpstreamAutoTune

	BlockInfo       *BlockInfo
	LowerBoundsInfo *LowerBoundInfo
}

func DefaultUpstreamState(upstreamMethods methods.Methods, caps mapset.Set[Cap], upstreamIndex string, rt *ratelimiter.RateLimitBudget, autoTuneRateLimiter *ratelimiter.UpstreamAutoTune) UpstreamState {
	return UpstreamState{
		Status:              Unavailable,
		UpstreamMethods:     upstreamMethods,
		BlockInfo:           NewBlockInfo(),
		LowerBoundsInfo:     NewLowerBoundInfo(),
		Caps:                caps,
		HeadData:            ZeroBlock{},
		UpstreamIndex:       upstreamIndex,
		RateLimiterBudget:   rt,
		AutoTuneRateLimiter: autoTuneRateLimiter,
	}
}
