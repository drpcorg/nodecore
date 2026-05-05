package protocol

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

// UpstreamRestRequest represents a REST call dispatched over the shared
// HttpConnector. The connector inspects RequestHolder.Method() and splits it on
// `MethodSeparator` ("#") into [verb, path]; the verb becomes the HTTP method
// and the path is appended to the upstream endpoint. Encoded query parameters
// can be baked into `path` directly (e.g. "/v2/blocks/123?header-only=true").
//
// The struct is intentionally minimal: spec-driven param parsing/modification,
// caching and request observation collapse to no-ops because internal REST
// calls (chain validation, lower-bound probes, label detection) do not flow
// through the spec/cache machinery used for client-facing JSON-RPC. Should a
// future change route external REST traffic here those can be filled in.
type UpstreamRestRequest struct {
	id            string
	method        string
	path          string
	body          []byte
	headers       map[string]string
	specMethod    *specs.Method
	observer      *RequestObserver
}

// NewInternalUpstreamRestRequest builds a REST request for one of nodecore's
// own internal calls (label/lower-bound/health/chain-id detection). The method
// is encoded as "<HTTP-verb>#<path>" as expected by HttpConnector.requestParams,
// and the path may contain a leading slash and an already-encoded query string.
func NewInternalUpstreamRestRequest(httpMethod, path string, chain chains.Chain) *UpstreamRestRequest {
	verb := strings.ToUpper(strings.TrimSpace(httpMethod))
	if verb == "" {
		verb = "GET"
	}
	cleanPath := path
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	combined := verb + MethodSeparator + cleanPath
	return &UpstreamRestRequest{
		id:       "1",
		method:   combined,
		path:     cleanPath,
		observer: NewRequestObserver(false).WithRequestKind(InternalUnary).WithMethod(combined),
	}
}

// NewInternalUpstreamRestRequestWithQuery is a convenience wrapper that encodes
// a map of query params alongside the path so callers don't have to assemble
// query strings manually.
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

func NewUpstreamRestRequest() *UpstreamRestRequest {
	return &UpstreamRestRequest{id: "1", method: "GET" + MethodSeparator + "/"}
}

func (u *UpstreamRestRequest) RequestObserver() *RequestObserver {
	return u.observer
}

func (u *UpstreamRestRequest) ModifyParams(_ context.Context, _ any) {
	// Internal REST calls are constructed in full at the call site - there is
	// no spec-driven param rewriting to perform here.
}

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
