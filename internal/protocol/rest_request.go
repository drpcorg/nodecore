package protocol

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/url"
	"sync"

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

	requestKeyOnce sync.Once
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

func NewUpstreamRestRequest(id, methodTemplate string, requestParams *RequestParams, body []byte, specName string, selectors ...RequestSelector) *UpstreamRestRequest {
	specMethod := specs.GetSpecMethodWithFallback(specName, methodTemplate)
	return &UpstreamRestRequest{
		id:            id,
		method:        methodTemplate,
		body:          body,
		requestParams: requestParams,
		observer:      NewRequestObserver(false).WithRequestKind(Unary).WithMethod(methodTemplate),
		specMethod:    specMethod,
		selectors:     selectors,
	}
}

func NewStreamUpstreamRestRequest(id, methodTemplate string, requestParams *RequestParams, body []byte, specName string, selectors ...RequestSelector) *UpstreamRestRequest {
	request := NewUpstreamRestRequest(id, methodTemplate, requestParams, body, specName, selectors...)
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
	u.requestKeyOnce.Do(func() {
		u.requestKey = calculateRestHash(u.method, u.requestParams, u.body, u.selectors)
	})
	return u.requestKey
}

func (u *UpstreamRestRequest) RequestParams() *RequestParams {
	return u.requestParams
}

func (u *UpstreamRestRequest) Selectors() []RequestSelector {
	return append([]RequestSelector(nil), u.selectors...)
}

// calculateRestHash derives a REST request's identity from everything that
// changes the response: the spec method template, the ordered path captures,
// the query, and the body. Headers are intentionally excluded - they carry
// auth/tracing, not resource identity, and would fragment the cache key per
// client. url.Values.Encode sorts by key so the digest is deterministic
// regardless of map iteration order.
//
// Each section is framed as tag(1 byte) + length(8-byte big-endian) + data.
// The length prefix makes the encoding injective, so the input families can
// never alias - e.g. a query value "a=" and a body "a=" (or a path capture
// "x" and a body "x") must not collide, which a bare separator would allow.
func calculateRestHash(method string, params *RequestParams, body []byte, selectors []RequestSelector) string {
	var buf bytes.Buffer
	writeSection := func(tag byte, data []byte) {
		var hdr [9]byte
		hdr[0] = tag
		binary.BigEndian.PutUint64(hdr[1:], uint64(len(data)))
		buf.Write(hdr[:])
		buf.Write(data)
	}
	writeSection('m', []byte(method))
	if params != nil {
		for _, p := range params.PathParams {
			writeSection('p', []byte(p))
		}
		if len(params.QueryParams) > 0 {
			writeSection('q', []byte(url.Values(params.QueryParams).Encode()))
		}
	}
	if len(body) > 0 {
		writeSection('b', body)
	}
	// The label key is the request's node-class identity (see LabelCacheKey), so
	// a labeled request can't read another class's cached response. It is empty
	// for class-unconstrained requests, leaving their hash unchanged.
	if labelKey := LabelCacheKey(selectors); labelKey != "" {
		writeSection('s', []byte(labelKey))
	}
	return calculateHash(buf.Bytes())
}

var _ RequestHolder = (*UpstreamRestRequest)(nil)
