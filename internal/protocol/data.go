package protocol

import (
	"fmt"
	"github.com/drpcorg/dshaltie/internal/upstreams/methods"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"io"
	"math"
)

func IsStream(method string) bool {
	// TODO: implement logic to determine if a method is streaming or not
	return method == "eth_getLogs" || method == "eth_getBlockByHash" || method == "getProgramAccounts"
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

type JsonRpcRequest struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

type RequestHolder interface {
	Id() string
	Method() string
	Headers() map[string]string
	Body() []byte
	IsStream() bool
	Count() int
	RequestType() RequestType
}

type ResponseHolder interface {
	ResponseResult() []byte
	GetError() *ResponseError
	EncodeResponse(realId []byte) io.Reader
	HasError() bool
	HasStream() bool
	Id() string
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
	Available AvailabilityStatus = iota
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
	default:
		panic(fmt.Sprintf("unknown status %d", a))
	}
}

type UpstreamEvent struct {
	Id    string
	Chain chains.Chain
	State *UpstreamState
}

type UpstreamState struct {
	Status          AvailabilityStatus
	HeadData        *BlockData
	UpstreamMethods methods.Methods
}

type AbstractUpstreamStateEvent interface {
	event()
}

type HeadUpstreamStateEvent struct {
	HeadData *BlockData
}

func (h *HeadUpstreamStateEvent) event() {}
