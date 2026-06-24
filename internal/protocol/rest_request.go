package protocol

import (
	"bytes"
	"context"
	"net/url"

	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

type UpstreamRestRequest struct {
	id            string
	method        string
	requestKey    string
	body          []byte
	requestParams *RequestParams
	specMethod    *specs.Method
	observer      *RequestObserver
	selectors     []RequestSelector
	isStream      bool
}

// NewInternalUpstreamRestRequest builds an internally-originated REST request
// from a spec method template ("GET#/v2/status", "GET#/v2/blocks/*") and the
// matching RequestParams. Wildcard captures live on requestParams.PathParams
// and query on requestParams.QueryParams - the connector rebuilds the literal
// upstream URL from template + captures via utils.BuildRestURL. This mirrors
// NewUpstreamRestRequest so internal probes carry the bounded template as their
// Method() (not a per-resource literal path) and resolve a real spec method.
func NewInternalUpstreamRestRequest(methodTemplate string, requestParams *RequestParams, chain chains.Chain) *UpstreamRestRequest {
	return NewInternalUpstreamRestRequestWithBody(methodTemplate, requestParams, nil, chain)
}

func NewInternalUpstreamRestRequestWithBody(methodTemplate string, requestParams *RequestParams, body []byte, chain chains.Chain) *UpstreamRestRequest {
	specName := chains.GetMethodSpecNameByChain(chain)
	return &UpstreamRestRequest{
		id:            "1",
		method:        methodTemplate,
		body:          body,
		requestParams: requestParams,
		observer:      NewRequestObserver(false).WithRequestKind(InternalUnary).WithMethod(methodTemplate),
		specMethod:    specs.GetSpecMethodWithFallback(specName, methodTemplate),
	}
}

func NewUpstreamRestRequest(id, methodTemplate string, requestParams *RequestParams, body []byte, specName string) *UpstreamRestRequest {
	specMethod := specs.GetSpecMethodWithFallback(specName, methodTemplate)
	return &UpstreamRestRequest{
		id:            id,
		method:        methodTemplate,
		requestKey:    calculateRestHash(methodTemplate, requestParams, body),
		body:          body,
		requestParams: requestParams,
		observer:      NewRequestObserver(false).WithRequestKind(Unary).WithMethod(methodTemplate),
		specMethod:    specMethod,
	}
}

func NewStreamUpstreamRestRequest(id, methodTemplate string, requestParams *RequestParams, body []byte, specName string) *UpstreamRestRequest {
	request := NewUpstreamRestRequest(id, methodTemplate, requestParams, body, specName)
	request.isStream = true
	return request
}

func (u *UpstreamRestRequest) RequestObserver() *RequestObserver {
	return u.observer
}

func (u *UpstreamRestRequest) ModifyParams(_ context.Context, _ any) {}

func (u *UpstreamRestRequest) SpecMethod() *specs.Method {
	return u.specMethod
}

func (u *UpstreamRestRequest) Id() string {
	return u.id
}

func (u *UpstreamRestRequest) Method() string {
	return u.method
}

func (u *UpstreamRestRequest) Body() ([]byte, error) {
	return u.body, nil
}

func (u *UpstreamRestRequest) ParseParams(_ context.Context) specs.MethodParam {
	return nil
}

func (u *UpstreamRestRequest) IsStream() bool {
	return u.isStream
}

func (u *UpstreamRestRequest) IsSubscribe() bool {
	return false
}

func (u *UpstreamRestRequest) RequestType() RequestType {
	return Rest
}

func (u *UpstreamRestRequest) RequestHash() string {
	return u.requestKey
}

func (u *UpstreamRestRequest) RequestParams() *RequestParams {
	return u.requestParams
}

func (u *UpstreamRestRequest) Selectors() []RequestSelector {
	return append([]RequestSelector(nil), u.selectors...)
}

func (u *UpstreamRestRequest) setSelectors(selectors []RequestSelector) {
	u.selectors = append([]RequestSelector(nil), selectors...)
}

// calculateRestHash derives a REST request's identity from everything that
// changes the response: the spec method template, the ordered path captures,
// the query, and the body. Headers are intentionally excluded - they carry
// auth/tracing, not resource identity, and would fragment the cache key per
// client. url.Values.Encode sorts by key so the digest is deterministic
// regardless of map iteration order.
func calculateRestHash(method string, params *RequestParams, body []byte) string {
	var buf bytes.Buffer
	buf.WriteString(method)
	if params != nil {
		for _, p := range params.PathParams {
			buf.WriteByte('\n')
			buf.WriteString(p)
		}
		if len(params.QueryParams) > 0 {
			buf.WriteByte('\n')
			buf.WriteString(url.Values(params.QueryParams).Encode())
		}
	}
	if len(body) > 0 {
		buf.WriteByte('\n')
		buf.Write(body)
	}
	return calculateHash(buf.Bytes())
}

var _ RequestHolder = (*UpstreamRestRequest)(nil)
