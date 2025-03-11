package protocol

import (
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

type HttpUpstreamRequest struct {
	id             interface{}
	method         string
	requestHeaders map[string]string
	requestBody    []byte
	isStream       bool
}

func NewHttpUpstreamRequest(
	method string,
	headers map[string]string,
	body []byte,
	stream bool,
) *HttpUpstreamRequest {
	return &HttpUpstreamRequest{
		id:             1,
		method:         method,
		requestHeaders: headers,
		requestBody:    body,
		isStream:       stream,
	}
}

func NewJsonRpcUpstreamRequest(id interface{}, method string, params []interface{}, stream bool) (*HttpUpstreamRequest, error) {
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
		isStream:    stream,
	}, nil
}

const MethodSeparator = "#"

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

func (h *HttpUpstreamRequest) Id() interface{} {
	return h.id
}
