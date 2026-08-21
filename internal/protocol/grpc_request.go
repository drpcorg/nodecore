package protocol

import (
	"context"
	"sync"

	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

// UpstreamGrpcRequest is a unary gRPC call as the proxy sees it: the full
// method string ("/sui.rpc.v2.LedgerService/GetObject"), the serialized
// request message exactly as the client produced it (the 5-byte gRPC wire
// frame prefix never appears at this layer - grpc-go adds and strips it below
// the codec), and the client metadata in RequestParams.Headers. The body is
// never parsed - the traffic path is bytes-only.
type UpstreamGrpcRequest struct {
	id            string
	method        string
	requestKey    string
	body          []byte
	requestParams *RequestParams
	specMethod    *specs.Method
	observer      *RequestObserver
	selectors     []RequestSelector

	requestKeyOnce sync.Once
}

// NewInternalUpstreamGrpcRequest builds an internally-originated gRPC request
// for chain-specific probes: the caller proto.Marshals a typed request into
// body and the connector sends it verbatim.
func NewInternalUpstreamGrpcRequest(method string, body []byte, chain chains.Chain) *UpstreamGrpcRequest {
	specName := chains.GetMethodSpecNameByChain(chain)
	return &UpstreamGrpcRequest{
		id:         "1",
		method:     method,
		body:       body,
		observer:   NewRequestObserver(false).WithRequestKind(InternalUnary).WithMethod(method),
		specMethod: specs.GetSpecMethodWithFallback(specName, method),
	}
}

func NewUpstreamGrpcRequest(id, method string, requestParams *RequestParams, body []byte, specName string, selectors ...RequestSelector) *UpstreamGrpcRequest {
	return &UpstreamGrpcRequest{
		id:            id,
		method:        method,
		body:          body,
		requestParams: requestParams,
		observer:      NewRequestObserver(false).WithRequestKind(Unary).WithMethod(method),
		specMethod:    specs.GetSpecMethodWithFallback(specName, method),
		selectors:     selectors,
	}
}

func (u *UpstreamGrpcRequest) RequestObserver() *RequestObserver {
	return u.observer
}

func (u *UpstreamGrpcRequest) ModifyParams(_ context.Context, _ any) {}

func (u *UpstreamGrpcRequest) SpecMethod() *specs.Method {
	return u.specMethod
}

func (u *UpstreamGrpcRequest) Id() string {
	return u.id
}

func (u *UpstreamGrpcRequest) Method() string {
	return u.method
}

func (u *UpstreamGrpcRequest) Body() ([]byte, error) {
	return u.body, nil
}

func (u *UpstreamGrpcRequest) ParseParams(_ context.Context) specs.MethodParam {
	return nil
}

func (u *UpstreamGrpcRequest) IsStream() bool {
	return false
}

func (u *UpstreamGrpcRequest) IsSubscribe() bool {
	return false
}

func (u *UpstreamGrpcRequest) RequestType() RequestType {
	return Grpc
}

// RequestHash reuses calculateRestHash's injective tag+length framing over
// exactly the sections a gRPC call has: method, body, and the selector label
// key. There are no path/query sections, and method shapes
// ("/package.Service/Method" vs "VERB#/path") keep the two families apart.
func (u *UpstreamGrpcRequest) RequestHash() string {
	u.requestKeyOnce.Do(func() {
		u.requestKey = calculateRestHash(u.method, nil, u.body, u.selectors)
	})
	return u.requestKey
}

func (u *UpstreamGrpcRequest) RequestParams() *RequestParams {
	return u.requestParams
}

func (u *UpstreamGrpcRequest) Selectors() []RequestSelector {
	return append([]RequestSelector(nil), u.selectors...)
}

var _ RequestHolder = (*UpstreamGrpcRequest)(nil)
