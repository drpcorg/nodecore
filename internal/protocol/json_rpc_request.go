package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog"
)

type UpstreamJsonRpcRequest struct {
	id              string
	realId          json.RawMessage
	method          string
	parsedParam     specs.MethodParam
	requestParams   json.RawMessage
	requestKey      string
	specMethod      *specs.Method
	requestObserver *RequestObserver

	parsed   bool
	isStream bool
	isSub    bool

	mu sync.Mutex
}

func NewUpstreamJsonRpcRequestWithSpecMethod(method string, params any, specMethod *specs.Method) (*UpstreamJsonRpcRequest, error) {
	requestParams, err := sonic.Marshal(params)
	if err != nil {
		return nil, err
	}
	return &UpstreamJsonRpcRequest{
		id:            "1",
		method:        method,
		realId:        []byte(`"1"`),
		requestParams: requestParams,
		specMethod:    specMethod,
	}, nil
}

func NewInternalUpstreamJsonRpcRequest(method string, params any, chain chains.Chain) (*UpstreamJsonRpcRequest, error) {
	requestParams, err := sonic.Marshal(params)
	if err != nil {
		return nil, err
	}
	specMethod := specs.GetSpecMethod(chains.GetMethodSpecNameByChain(chain), method)
	return &UpstreamJsonRpcRequest{
		id:              "1",
		method:          method,
		realId:          []byte(`"1"`),
		requestParams:   requestParams,
		specMethod:      specMethod,
		requestObserver: NewRequestObserver(false).WithRequestKind(InternalUnary).WithMethod(method),
	}, nil
}

func NewInternalSubUpstreamJsonRpcRequest(method string, params any, chain chains.Chain) (*UpstreamJsonRpcRequest, error) {
	requestParams, err := sonic.Marshal(params)
	if err != nil {
		return nil, err
	}
	specMethod := specs.GetSpecMethod(chains.GetMethodSpecNameByChain(chain), method)
	return &UpstreamJsonRpcRequest{
		id:              "1",
		method:          method,
		realId:          []byte(`"1"`),
		requestParams:   requestParams,
		isSub:           true,
		specMethod:      specMethod,
		requestObserver: NewRequestObserver(true).WithRequestKind(InternalSubscription).WithMethod(method),
	}, nil
}

func NewUpstreamJsonRpcRequest(id string, jsonRpcRequest JsonRpcRequestBody, isSub bool, specName string) *UpstreamJsonRpcRequest {
	specMethod := specs.GetSpecMethodWithFallback(specName, jsonRpcRequest.Method)
	return &UpstreamJsonRpcRequest{
		id:              id,
		method:          jsonRpcRequest.Method,
		realId:          jsonRpcRequest.Id,
		requestParams:   jsonRpcRequest.Params,
		isSub:           isSub,
		requestKey:      calculateJsonRpcHash(jsonRpcRequest.Method, jsonRpcRequest.Params),
		specMethod:      specMethod,
		requestObserver: NewRequestObserver(isSub).WithMethod(jsonRpcRequest.Method),
	}
}

func NewStreamUpstreamJsonRpcRequest(id string, jsonRpcRequest JsonRpcRequestBody, specName string) *UpstreamJsonRpcRequest {
	request := NewUpstreamJsonRpcRequest(id, jsonRpcRequest, false, specName)
	request.isStream = true
	return request
}

func calculateJsonRpcHash(method string, params json.RawMessage) string {
	var requestHash string
	if len(params) == 0 {
		requestHash = calculateHash([]byte(method))
	} else {
		requestHash = calculateHash(append(params, []byte(method)...))
	}
	return requestHash
}

func (u *UpstreamJsonRpcRequest) Id() string {
	return u.id
}

// RealId returns the textual form of the JSON-RPC id actually placed on the
// wire (u.realId), unwrapped from the outer JSON quotes for string ids. This
// is what upstreams echo back in correlation headers (e.g. the QR quorum
// signature headers), and it differs from Id() which is nodecore's internal
// UUID tag used to route responses.
func (u *UpstreamJsonRpcRequest) RealId() string {
	raw := bytes.TrimSpace(u.realId)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' && raw[len(raw)-1] == '"' {
		var s string
		if err := sonic.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

func (u *UpstreamJsonRpcRequest) Method() string {
	return u.method
}

func (u *UpstreamJsonRpcRequest) Headers() map[string]string {
	return nil
}

func (u *UpstreamJsonRpcRequest) Body() ([]byte, error) {
	body, err := jsonRpcRequestBytes(u.realId, u.method, u.requestParams)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (u *UpstreamJsonRpcRequest) ParseParams(ctx context.Context) specs.MethodParam {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.specMethod == nil {
		return nil
	}
	if u.parsedParam != nil && u.parsed {
		return u.parsedParam
	}
	var requestParams any
	if len(u.requestParams) == 0 {
		requestParams = []any{}
	} else {
		err := sonic.Unmarshal(u.requestParams, &requestParams)
		if err != nil {
			return nil
		}
	}

	parsedParam := u.specMethod.Parse(ctx, requestParams)
	u.parsedParam = parsedParam
	u.parsed = true

	return parsedParam
}

func (u *UpstreamJsonRpcRequest) ModifyParams(ctx context.Context, newValue any) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.specMethod == nil {
		return
	}

	var requestParams any
	if len(u.requestParams) == 0 {
		requestParams = []any{}
	} else {
		err := sonic.Unmarshal(u.requestParams, &requestParams)
		if err != nil {
			zerolog.Ctx(ctx).
				Warn().
				Err(err).
				Msgf("couldn't unmarshall a request, method - %s, body - %s", u.method, string(u.requestParams))
			return
		}
	}

	modifiedData := u.specMethod.Modify(ctx, requestParams, newValue)
	if len(modifiedData) > 0 {
		u.requestParams = modifiedData
		u.parsed = false
	}
}

func (u *UpstreamJsonRpcRequest) IsStream() bool {
	return u.isStream
}

func (u *UpstreamJsonRpcRequest) IsSubscribe() bool {
	return u.isSub
}

func (u *UpstreamJsonRpcRequest) RequestType() RequestType {
	return JsonRpc
}

func (u *UpstreamJsonRpcRequest) RequestHash() string {
	return u.requestKey
}

func (u *UpstreamJsonRpcRequest) SpecMethod() *specs.Method {
	return u.specMethod
}

func (u *UpstreamJsonRpcRequest) RequestObserver() *RequestObserver {
	return u.requestObserver
}

func (u *UpstreamJsonRpcRequest) RequestParams() *RequestParams {
	return nil
}

var _ RequestHolder = (*UpstreamJsonRpcRequest)(nil)
