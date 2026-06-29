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
	"github.com/drpcorg/nodecore/pkg/utils"
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
	SafeBlock
)

func (b BlockType) String() string {
	switch b {
	case FinalizedBlock:
		return "finalized"
	case SafeBlock:
		return "safe"
	default:
		panic(fmt.Sprintf("unknown blockType %d", b))
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

// RequestParams carries the non-body components of a REST request as the
// proxy sees them. Headers and QueryParams use []string values so that
// repeated keys (e.g. "?ids=a&ids=b" or a header appearing twice) survive the
// round-trip - the upstream sees them just like the client sent them.
//
// PathParams holds the wildcard captures from the spec's URL template in path
// order. For HTTP requests they come out of PathMatcher; for gRPC NativeCall
// they come straight from the protobuf RestData.path_params field.
type RequestParams struct {
	Headers     map[string][]string
	QueryParams map[string][]string
	PathParams  []string
}

type RequestHolder interface {
	Id() string
	Method() string
	RequestParams() *RequestParams
	Body() ([]byte, error)
	ParseParams(ctx context.Context) specs.MethodParam
	RequestType() RequestType
	RequestHash() string
	SpecMethod() *specs.Method
	RequestObserver() *RequestObserver
	Selectors() []RequestSelector

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
	// GetStreamHint returns the streaming hint located by the connector's
	// single-pass first-chunk analysis, or nil when the response carries no
	// hint (non-streaming, REST, errors, subscriptions). Only the gRPC
	// result-unwrap consumer uses it.
	GetStreamHint() StreamHint
	Id() string
}

type SubscriptionResponseHolder interface {
	ResponseHolder
	IsEventFrame() bool
}

type RequestBlockTag int

const (
	BlockTagLatest RequestBlockTag = iota
	BlockTagSafe
	BlockTagFinalized
)

// RequestSelector is a sealed protocol-neutral selector sum type. Keep concrete
// selector payloads separate so invalid field combinations (for example both a
// block tag and numeric height) cannot be represented by construction.
type RequestSelector interface {
	isRequestSelector()
	Key() string
}

type RequestAnySelector struct{}

func (RequestAnySelector) isRequestSelector() {}

type RequestLabelSelector struct {
	Name   string
	Values []string
}

func (RequestLabelSelector) isRequestSelector() {}

type RequestExistsSelector struct {
	Name string
}

func (RequestExistsSelector) isRequestSelector() {}

type RequestAndSelector struct {
	Children []RequestSelector
}

func (RequestAndSelector) isRequestSelector() {}

type RequestOrSelector struct {
	Children []RequestSelector
}

func (RequestOrSelector) isRequestSelector() {}

type RequestNotSelector struct {
	Child RequestSelector
}

func (RequestNotSelector) isRequestSelector() {}

type RequestHeightSelector struct {
	Height int64
}

func (RequestHeightSelector) isRequestSelector() {}

type RequestBlockTagSelector struct {
	Tag RequestBlockTag
}

func (RequestBlockTagSelector) isRequestSelector() {}

type RequestSlotHeightSelector struct {
	SlotHeight int64
}

func (RequestSlotHeightSelector) isRequestSelector() {}

type RequestLowerHeightSelector struct {
	Height         int64
	LowerBoundType LowerBoundType
	TimeOffset     int64
	HeightDelta    int64
}

func (RequestLowerHeightSelector) isRequestSelector() {}

type RequestUnsupportedSelector struct {
	Reason string
}

func (RequestUnsupportedSelector) isRequestSelector() {}

type UpstreamSubscriptionResponse interface {
	ResponseChan() chan *WsResponse
	OpId() string
}

type ResponseHolderWrapper struct {
	UpstreamId            string
	RequestId             string
	UpstreamNodeVersion   string
	FinalizationBlockType *BlockType
	FinalizationBlock     Block
	Response              ResponseHolder
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

type HeadUpstreamEvent struct {
	Status AvailabilityStatus
	Head   Block
}

func (h HeadUpstreamEvent) eventData() {}

type RemoveUpstreamEvent struct{}

func (r RemoveUpstreamEvent) eventData() {}

type ValidUpstreamEvent struct{}

func (r ValidUpstreamEvent) eventData() {}

type Cap int

const (
	// WsCap means the upstream has a live websocket connector (any chain). Used
	// to route node-backed subscriptions to a ws-capable upstream.
	WsCap Cap = iota
	// NewHeadsCap means the upstream's head is subscription-driven (its head
	// connector is a websocket), so newHeads can be synthesized locally from the
	// head stream. EVM-only.
	NewHeadsCap
	// LogsCap means NewHeadsCap holds and eth_getLogs is enabled, so logs can be
	// synthesized locally. EVM-only.
	LogsCap
	// PendingTxCap means the upstream has a live websocket connector usable for
	// eth_subscribe("newPendingTransactions"), so pending-tx subscriptions can be
	// aggregated locally. Governed by the cap pipeline: a live ws connector is enough
	// on most chains, but on chains with a pending-tx validator (e.g. BASE) it is also
	// gated on txpool_content showing a non-trivial mempool. The topics are EVM-only
	// but the cap is harmless elsewhere since only eth_subscribe clients request them.
	PendingTxCap
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
	Labels          *Labels
}

func DefaultUpstreamState(upstreamMethods methods.Methods, caps mapset.Set[Cap], upstreamIndex string, rt *ratelimiter.RateLimitBudget, autoTuneRateLimiter *ratelimiter.UpstreamAutoTune) UpstreamState {
	return UpstreamState{
		Status:              Available,
		UpstreamMethods:     upstreamMethods,
		BlockInfo:           NewBlockInfo(),
		LowerBoundsInfo:     NewLowerBoundInfo(),
		Labels:              NewLabels(),
		Caps:                caps,
		HeadData:            ZeroBlock{},
		UpstreamIndex:       upstreamIndex,
		RateLimiterBudget:   rt,
		AutoTuneRateLimiter: autoTuneRateLimiter,
	}
}

type Labels struct {
	labels *utils.CMap[string, string]
}

func NewLabels() *Labels {
	return &Labels{
		labels: utils.NewCMap[string, string](),
	}
}

func (l *Labels) GetLabel(label string) (string, bool) {
	return l.labels.Load(label)
}

func (l *Labels) AddLabel(key, value string) {
	l.labels.Store(key, value)
}

func (l *Labels) GetAllLabels() map[string]string {
	labels := make(map[string]string, 10)

	l.labels.Range(func(k, v string) bool {
		labels[k] = v
		return true
	})

	return labels
}

func (l *Labels) Copy() *Labels {
	newLabels := NewLabels()
	l.labels.Range(func(k, v string) bool {
		newLabels.AddLabel(k, v)
		return true
	})
	return newLabels
}
