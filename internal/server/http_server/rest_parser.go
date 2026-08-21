package http_server

import (
	"net/http"
	"unicode/utf8"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
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
// Returns errNonUtf8Method when the resulting methodTemplate is not valid
// UTF-8, which can only happen on the fallback branch above.
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

	// One check covers both switch branches. The matched-template branch passes
	// trivially - templates come from the embedded spec JSON - so in practice this
	// only ever rejects the "<VERB>#/<restPath>" fallback, where the client's bytes
	// become the method name. Wildcard captures, headers, and query values are not
	// checked: they never become a method name or a metric label.
	if !utf8.ValidString(methodTemplate) {
		return "", nil, errNonUtf8Method
	}

	requestParams = &protocol.RequestParams{
		PathParams:  params,
		Headers:     server_ctx.SanitizeForwardedHeaders(req.Header),
		QueryParams: filteredQuery(req.URL.Query()),
	}

	return methodTemplate, requestParams, nil
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
