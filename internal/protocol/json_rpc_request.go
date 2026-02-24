package protocol

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/bytedance/sonic"
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

func NewInternalUpstreamJsonRpcRequest(method string, params any) (*UpstreamJsonRpcRequest, error) {
	requestParams, err := sonic.Marshal(params)
	if err != nil {
		return nil, err
	}
	return &UpstreamJsonRpcRequest{
		id:              "1",
		method:          method,
		realId:          []byte(`"1"`),
		requestParams:   requestParams,
		requestObserver: NewRequestObserver(false).WithRequestKind(InternalUnary).WithMethod(method),
	}, nil
}

func NewInternalSubUpstreamJsonRpcRequest(method string, params any) (*UpstreamJsonRpcRequest, error) {
	requestParams, err := sonic.Marshal(params)
	if err != nil {
		return nil, err
	}
	return &UpstreamJsonRpcRequest{
		id:              "1",
		method:          method,
		realId:          []byte(`"1"`),
		requestParams:   requestParams,
		isSub:           true,
		requestObserver: NewRequestObserver(true).WithRequestKind(InternalSubscription).WithMethod(method),
	}, nil
}

func NewUpstreamJsonRpcRequest(
	id string,
	realId json.RawMessage,
	method string,
	params json.RawMessage,
	isSub bool,
	specMethod *specs.Method,
) *UpstreamJsonRpcRequest {
	return &UpstreamJsonRpcRequest{
		id:              id,
		method:          method,
		realId:          realId,
		requestParams:   params,
		isSub:           isSub,
		requestKey:      calculateJsonRpcHash(method, params),
		specMethod:      specMethod,
		requestObserver: NewRequestObserver(isSub).WithMethod(method),
	}
}

func NewStreamUpstreamJsonRpcRequest(id string, realId json.RawMessage, method string, params json.RawMessage, specMethod *specs.Method) *UpstreamJsonRpcRequest {
	return &UpstreamJsonRpcRequest{
		id:              id,
		method:          method,
		realId:          realId,
		requestParams:   params,
		isStream:        true,
		requestKey:      calculateJsonRpcHash(method, params),
		specMethod:      specMethod,
		requestObserver: NewRequestObserver(false).WithMethod(method),
	}
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

var _ RequestHolder = (*UpstreamJsonRpcRequest)(nil)
