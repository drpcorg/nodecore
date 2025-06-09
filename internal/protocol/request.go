package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bytedance/sonic"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/ethereum/go-ethereum/crypto/blake2b"
	"sync"
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

type BaseUpstreamRequest struct {
	id             string
	method         string
	requestHeaders map[string]string
	requestBody    []byte
	requestParams  any // for json-rpc it's params array or object
	parsedParam    specs.MethodParam
	isStream       bool
	isSub          bool
	requestType    RequestType
	requestKey     string

	mu sync.Mutex
}

var _ RequestHolder = (*BaseUpstreamRequest)(nil)

func NewHttpUpstreamRequest(
	method string,
	headers map[string]string,
	body []byte,
) *BaseUpstreamRequest {
	return newRestRequest(method, headers, body, false)
}

func NewStreamRestUpstreamRequest(
	method string,
	headers map[string]string,
	body []byte,
) *BaseUpstreamRequest {
	return newRestRequest(method, headers, body, true)
}

func newRestRequest(method string, headers map[string]string, body []byte, stream bool) *BaseUpstreamRequest {
	requestBytes := body
	if len(requestBytes) == 0 {
		requestBytes = []byte(method)
	}

	return &BaseUpstreamRequest{
		id:             "1",
		method:         method,
		requestHeaders: headers,
		requestBody:    body,
		isStream:       stream,
		requestType:    Rest,
		requestParams:  []any{},
		requestKey:     calculateHash(requestBytes),
	}
}

func NewInternalJsonRpcUpstreamRequest(method string, params any) (*BaseUpstreamRequest, error) {
	jsonRpcReq := map[string]interface{}{
		"id":      "1",
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	jsonRpcReqBytes, err := sonic.Marshal(jsonRpcReq)
	if err != nil {
		return nil, err
	}

	return &BaseUpstreamRequest{
		id:            "1",
		method:        method,
		requestBody:   jsonRpcReqBytes,
		requestType:   JsonRpc,
		requestParams: []any{},
	}, nil
}

func NewSimpleJsonRpcUpstreamRequest(id string, realId json.RawMessage, method string, params json.RawMessage, isSub bool) (*BaseUpstreamRequest, error) {
	return newJsonRpcRequest(id, realId, method, params, false, isSub)
}

func NewStreamJsonRpcUpstreamRequest(id string, realId json.RawMessage, method string, params json.RawMessage) (*BaseUpstreamRequest, error) {
	return newJsonRpcRequest(id, realId, method, params, true, false)
}

func newJsonRpcRequest(id string, realId json.RawMessage, method string, params json.RawMessage, stream bool, isSub bool) (*BaseUpstreamRequest, error) {
	jsonRpcReqBytes, err := jsonRpcRequestBytes(realId, method, params)
	if err != nil {
		return nil, err
	}

	var requestHash string
	if len(params) == 0 {
		requestHash = calculateHash([]byte(method))
	} else {
		requestHash = calculateHash(append(params, []byte(method)...))
	}

	var requestParams any
	if len(params) == 0 {
		requestParams = []any{}
	} else {
		err = sonic.Unmarshal(params, &requestParams)
		if err != nil {
			return nil, err
		}
	}

	return &BaseUpstreamRequest{
		id:            id,
		method:        method,
		requestBody:   jsonRpcReqBytes,
		requestType:   JsonRpc,
		requestKey:    requestHash,
		isStream:      stream,
		requestParams: requestParams,
		isSub:         isSub,
	}, nil
}

func calculateHash(bytes []byte) string {
	hash := blake2b.Sum256(bytes)
	return fmt.Sprintf("%x", hash)
}

func jsonRpcRequestBytes(id json.RawMessage, method string, params json.RawMessage) ([]byte, error) {
	request := newJsonRpcRequestBody(id, method, params)
	requestBytes, err := sonic.Marshal(request)
	if err != nil {
		return nil, err
	}
	return requestBytes, nil
}

const MethodSeparator = "#"

func (h *BaseUpstreamRequest) RequestType() RequestType {
	return h.requestType
}

func (h *BaseUpstreamRequest) Method() string {
	return h.method
}

func (h *BaseUpstreamRequest) Headers() map[string]string {
	return h.requestHeaders
}

func (h *BaseUpstreamRequest) Body() []byte {
	return h.requestBody
}

func (h *BaseUpstreamRequest) RequestHash() string {
	return h.requestKey
}

func (h *BaseUpstreamRequest) IsStream() bool {
	return h.isStream
}

func (h *BaseUpstreamRequest) IsSubscribe() bool {
	return h.isSub
}

func (h *BaseUpstreamRequest) ParseParams(ctx context.Context, method *specs.Method) specs.MethodParam {
	h.mu.Lock()
	defer h.mu.Unlock()

	if method == nil {
		return nil
	}
	if h.parsedParam != nil {
		return h.parsedParam
	}

	parsedParam := method.Parse(ctx, h.requestParams)
	h.parsedParam = parsedParam

	return parsedParam
}

func (h *BaseUpstreamRequest) Id() string {
	return h.id
}
