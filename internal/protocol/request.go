package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/bytedance/sonic"
)

type HttpMethod int

const (
	Get HttpMethod = iota
	Post
)

func (h HttpMethod) String() string {
	switch h {
	case Post:
		return "POST"
	case Get:
		return "GET"
	}
	return ""
}

type RequestType int

const (
	Rest RequestType = iota
	JsonRpc
	Ws
	Grpc
	Unknown
)

func (r RequestType) String() string {
	switch r {
	case Rest:
		return "rest"
	case JsonRpc:
		return "json-rpc"
	case Ws:
		return "ws"
	case Unknown:
		return "unknown"
	case Grpc:
		return "grpc"
	}
	panic(fmt.Sprintf("unknown RequestType - %d", r))
}

type HttpUpstreamRequest struct {
	id             string
	method         string
	requestHeaders map[string]string
	requestBody    []byte
	isStream       bool
	requestType    RequestType
}

var _ RequestHolder = (*HttpUpstreamRequest)(nil)

func NewHttpUpstreamRequest(
	method string,
	headers map[string]string,
	body []byte,
	stream bool,
) *HttpUpstreamRequest {
	return &HttpUpstreamRequest{
		id:             "1",
		method:         method,
		requestHeaders: headers,
		requestBody:    body,
		isStream:       stream,
	}
}

func NewStreamRestUpstreamRequest(
	method string,
	headers map[string]string,
	body []byte,
) *HttpUpstreamRequest {
	return &HttpUpstreamRequest{
		id:             "1",
		method:         method,
		requestHeaders: headers,
		requestBody:    body,
		isStream:       true,
		requestType:    Rest,
	}
}

func NewJsonRpcUpstreamRequest(id string, method string, params any) (*HttpUpstreamRequest, error) {
	jsonRpcReq := map[string]interface{}{
		"id":      id,
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	jsonRpcReqBytes, err := sonic.Marshal(jsonRpcReq)
	if err != nil {
		return nil, err
	}

	return &HttpUpstreamRequest{
		id:          id,
		method:      method,
		requestBody: jsonRpcReqBytes,
		requestType: JsonRpc,
	}, nil
}

func NewStreamJsonRpcUpstreamRequest(id string, realId json.RawMessage, method string, params any) (*HttpUpstreamRequest, error) {
	jsonRpcReq := map[string]interface{}{
		"id":      realId,
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	jsonRpcReqBytes, err := sonic.Marshal(jsonRpcReq)
	if err != nil {
		return nil, err
	}

	return &HttpUpstreamRequest{
		id:          id,
		method:      method,
		requestBody: jsonRpcReqBytes,
		isStream:    true,
		requestType: JsonRpc,
	}, nil
}

const MethodSeparator = "#"

func (h *HttpUpstreamRequest) RequestType() RequestType {
	return h.requestType
}

func (h *HttpUpstreamRequest) Count() int {
	return 1
}

func (h *HttpUpstreamRequest) Method() string {
	return h.method
}

func (h *HttpUpstreamRequest) Headers() map[string]string {
	return h.requestHeaders
}

func (h *HttpUpstreamRequest) Body() []byte {
	return h.requestBody
}

func (h *HttpUpstreamRequest) IsStream() bool {
	return h.isStream
}

func (h *HttpUpstreamRequest) Id() string {
	return h.id
}
