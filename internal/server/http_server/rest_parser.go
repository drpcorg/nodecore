package http_server

import (
	"net/http"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

// reservedQueryParams names every query-string key that nodecore consumes for
// its own routing/control plane and therefore must NOT be forwarded to the
// upstream. Today that is just the quorum read parameters parsed by
// quorum.ParamsFromQuery; when new control params are introduced, add them
// here so the REST parser keeps stripping the right set in one place.
var reservedQueryParams = mapset.NewThreadUnsafeSet[string](
	"quorum",
	"quorum_required",
)

// parseRestRequest extracts the canonical method template, the wildcard
// captures, and the forwarded headers/query the upstream should see.
//
//   - methodTemplate is the spec template that fullPath matched, e.g.
//     "GET#/v2/accounts/*". For specs without REST routes (logical-name
//     specs like Algorand's) we fall back to "<verb>#<path>" so the request
//     still flows; the upstream URL is built from the literal path either way.
//   - pathParams holds the wildcard captures in path order; empty in the
//     fallback case above.
//   - requestParams carries the headers and query the client supplied, with
//     reservedQueryParams stripped so nodecore's own control plane never
//     leaks downstream.
//
// Returns errRestPathNotFound when the spec has REST routes but the path
// matches none of them. The HTTP layer maps that to a 404 / parse error.
func parseRestRequest(req *http.Request, restPath, specName string) (
	methodTemplate string,
	requestParams *protocol.RequestParams,
	err error,
) {
	fullPath := req.Method + protocol.MethodSeparator + "/" + restPath

	template, params, ok := specs.MatchRestMethod(specName, fullPath)
	switch {
	case ok:
		methodTemplate = template
	default:
		// Spec doesn't model REST routes (yet). Use the literal as the
		// canonical name so stats/caching key off something deterministic.
		methodTemplate = fullPath
	}

	requestParams = &protocol.RequestParams{
		PathParams:  params,
		Headers:     cloneHeaders(req.Header),
		QueryParams: filteredQuery(req.URL.Query()),
	}

	return methodTemplate, requestParams, nil
}

// cloneHeaders deep-copies http.Header into the protocol-level map. The
// http.Header type is already map[string][]string under the hood; we copy
// the slices so later mutation of either side doesn't bleed into the other.
func cloneHeaders(src http.Header) map[string][]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string][]string, len(src))
	for k, vs := range src {
		copied := make([]string, len(vs))
		copy(copied, vs)
		out[k] = copied
	}
	return out
}

// filteredQuery returns the request query with reservedQueryParams removed,
// preserving the multi-value semantics of url.Values. A nil result is
// returned for empty input so RequestParams.QueryParams stays nil rather
// than an empty map - a small but visible signal in logs.
func filteredQuery(q map[string][]string) map[string][]string {
	if len(q) == 0 {
		return nil
	}
	out := make(map[string][]string, len(q))
	for k, vs := range q {
		if reservedQueryParams.Contains(k) {
			continue
		}
		copied := make([]string, len(vs))
		copy(copied, vs)
		out[k] = copied
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
