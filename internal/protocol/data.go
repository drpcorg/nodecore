package protocol

import (
	"io"
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
