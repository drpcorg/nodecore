package protocol

import (
	"fmt"
	"github.com/drpcorg/dshaltie/internal/upstreams/methods"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"io"
	"math"
)

type ApiConnectorType int

const (
	JsonRpcConnector ApiConnectorType = iota
	RestConnector
	GrpcConnector
	WsConnector
)

type JsonRpcRequest struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

type UpstreamRequest interface {
	Id() interface{}
	Method() string
	Headers() map[string]string
	Body() []byte
	IsStream() bool
}

type UpstreamResponse interface {
	ResponseResult() []byte
	ResponseError() *UpstreamError
	EncodeResponse() io.Reader
	HasError() bool
	Id() interface{}
}

type UpstreamSubscriptionResponse interface {
	ResponseChan() chan *WsResponse
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
