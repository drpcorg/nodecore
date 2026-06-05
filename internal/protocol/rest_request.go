package protocol

import (
	"context"
	"net/url"
	"strings"

	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

type UpstreamRestRequest struct {
	id            string
	method        string
	body          []byte
	requestParams *RequestParams
	specMethod    *specs.Method
	observer      *RequestObserver
	selectors     []RequestSelector
	isStream      bool
}

func NewInternalUpstreamRestRequest(httpMethod, path string, chain chains.Chain) *UpstreamRestRequest {
	return NewInternalUpstreamRestRequestWithBody(httpMethod, path, nil, chain)
}

func NewInternalUpstreamRestRequestWithBody(httpMethod, path string, body []byte, chain chains.Chain) *UpstreamRestRequest {
	verb := normaliseVerb(httpMethod)
	cleanPath, queryParams := extractQuery(normalisePath(path))
	method := verb + MethodSeparator + cleanPath
	var rp *RequestParams
	if len(queryParams) > 0 {
		rp = &RequestParams{QueryParams: queryParams}
	}
	specMethod := specs.GetSpecMethod(chains.GetMethodSpecNameByChain(chain), method)
	return &UpstreamRestRequest{
		id:            "1",
		method:        method,
		requestParams: rp,
		observer:      NewRequestObserver(false).WithRequestKind(InternalUnary).WithMethod(method),
		specMethod:    specMethod,
		body:          body,
	}
}

func NewInternalUpstreamRestRequestWithQuery(httpMethod, path string, query map[string]string, chain chains.Chain) *UpstreamRestRequest {
	req := NewInternalUpstreamRestRequest(httpMethod, path, chain)
	if len(query) == 0 {
		return req
	}
	if req.requestParams == nil {
		req.requestParams = &RequestParams{}
	}
	if req.requestParams.QueryParams == nil {
		req.requestParams.QueryParams = make(map[string][]string, len(query))
	}
	for k, v := range query {
		req.requestParams.QueryParams[k] = append(req.requestParams.QueryParams[k], v)
	}
	return req
}

func NewUpstreamRestRequest(id, methodTemplate string, requestParams *RequestParams, body []byte, specName string) *UpstreamRestRequest {
	specMethod := specs.GetSpecMethodWithFallback(specName, methodTemplate)
	return &UpstreamRestRequest{
		id:            id,
		method:        methodTemplate,
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

func normaliseVerb(httpMethod string) string {
	verb := strings.ToUpper(strings.TrimSpace(httpMethod))
	if verb == "" {
		return "GET"
	}
	return verb
}

func normalisePath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

// extractQuery splits "?k=v&..." off the end of a path. A malformed query
// is dropped silently - internal callers build these strings themselves
// from format specifiers, so a parse error is a programmer bug, not a
// user-supplied payload to surface.
func extractQuery(path string) (cleanPath string, query map[string][]string) {
	base, raw, hasQuery := strings.Cut(path, "?")
	if !hasQuery || raw == "" {
		return path, nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) == 0 {
		return base, nil
	}
	return base, values
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
	return calculateHash([]byte(u.method))
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

var _ RequestHolder = (*UpstreamRestRequest)(nil)
