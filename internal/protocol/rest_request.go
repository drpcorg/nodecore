package protocol

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/google/uuid"
)

type UpstreamRestRequest struct {
	id         string
	method     string
	path       string
	body       []byte
	headers    map[string]string
	specMethod *specs.Method
	observer   *RequestObserver
}

func NewInternalUpstreamRestRequest(httpMethod, path string, chain chains.Chain) *UpstreamRestRequest {
	verb := normaliseVerb(httpMethod)
	cleanPath := normalisePath(path)
	combined := verb + MethodSeparator + cleanPath
	return &UpstreamRestRequest{
		id:       "1",
		method:   combined,
		path:     cleanPath,
		observer: NewRequestObserver(false).WithRequestKind(InternalUnary).WithMethod(combined),
	}
}

func NewInternalUpstreamRestRequestWithQuery(httpMethod, path string, query map[string]string, chain chains.Chain) *UpstreamRestRequest {
	if len(query) == 0 {
		return NewInternalUpstreamRestRequest(httpMethod, path, chain)
	}
	values := url.Values{}
	for k, v := range query {
		values.Set(k, v)
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	full := fmt.Sprintf("%s%s%s", path, separator, values.Encode())
	return NewInternalUpstreamRestRequest(httpMethod, full, chain)
}

// NewUpstreamRestRequest builds an external-facing REST request from an
// incoming HTTP call. Encodes method as "<VERB>#<path>" so the shared
// HttpConnector can split on MethodSeparator and forward the call to the
// upstream. Always initialises the observer to avoid a nil deref in
// ObserverConnector.
func NewUpstreamRestRequest(httpMethod, path string, body []byte) *UpstreamRestRequest {
	verb := normaliseVerb(httpMethod)
	cleanPath := normalisePath(path)
	combined := verb + MethodSeparator + cleanPath
	return &UpstreamRestRequest{
		id:       uuid.NewString(),
		method:   combined,
		path:     cleanPath,
		body:     body,
		observer: NewRequestObserver(false).WithRequestKind(Unary).WithMethod(combined),
	}
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

func (u *UpstreamRestRequest) Headers() map[string]string {
	return u.headers
}

func (u *UpstreamRestRequest) Body() ([]byte, error) {
	return u.body, nil
}

func (u *UpstreamRestRequest) ParseParams(_ context.Context) specs.MethodParam {
	return nil
}

func (u *UpstreamRestRequest) IsStream() bool {
	return false
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

var _ RequestHolder = (*UpstreamRestRequest)(nil)
