package server_ctx

import (
	"fmt"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
)

// The routing headers let a client restrict one request to a config
// group-label or to named upstreams. They arrive as HTTP headers (also on a
// WebSocket handshake) and as gRPC chain-ingress metadata.
const (
	xNodecoreGroupLabel = "X-Nodecore-Group-Label"
	xNodecoreUpstreamId = "X-Nodecore-Upstream-Id"
)

// RoutingSelectors reads the routing headers through values (http.Header.Values
// or metadata.MD.Get) and returns the selectors they stand for, or nil when
// neither header is present.
func RoutingSelectors(values func(name string) []string) []protocol.RequestSelector {
	var selectors []protocol.RequestSelector
	if groupLabel := groupLabelFrom(values(xNodecoreGroupLabel)); groupLabel != "" {
		selectors = append(selectors, protocol.RequestGroupLabelSelector{Labels: []string{groupLabel}, FromRoutingHeader: true})
	}
	if upstreamIds := upstreamIdsFrom(values(xNodecoreUpstreamId)); !upstreamIds.IsEmpty() {
		selectors = append(selectors, protocol.RequestUpstreamIdSelector{Ids: upstreamIds})
	}
	return selectors
}

// routingHeadersEnabled reports whether server.routing-headers is on. An app
// context built without a config has the headers off, like the default.
func (a *ApplicationServerContext) routingHeadersEnabled() bool {
	return a.AppConfig != nil && a.AppConfig.ServerConfig != nil && a.AppConfig.ServerConfig.RoutingHeaders
}

// appendRoutingSelectors restricts the request to what the client's routing
// headers admit. It fails closed: a request that cannot carry the selectors is
// rejected rather than served unrestricted.
func appendRoutingSelectors(request protocol.RequestHolder, selectors []protocol.RequestSelector) error {
	if len(selectors) == 0 {
		return nil
	}
	appender, ok := request.(protocol.SelectorAppender)
	if !ok {
		return fmt.Errorf("request of type %T cannot be restricted by routing headers", request)
	}
	appender.AppendSelectors(selectors...)
	return nil
}

// groupLabelFrom takes the header as one label, verbatim: repeated header
// lines are equivalent to one comma-joined line, and the value is never split.
func groupLabelFrom(headerValues []string) string {
	return strings.TrimSpace(strings.Join(headerValues, ", "))
}

// upstreamIdsFrom collects the ids of every header line, each of which may
// itself be a comma-separated list.
func upstreamIdsFrom(headerValues []string) mapset.Set[string] {
	upstreamIds := mapset.NewThreadUnsafeSet[string]()
	for _, headerValue := range headerValues {
		for part := range strings.SplitSeq(headerValue, ",") {
			if upstreamId := strings.TrimSpace(part); upstreamId != "" {
				upstreamIds.Add(upstreamId)
			}
		}
	}
	return upstreamIds
}
