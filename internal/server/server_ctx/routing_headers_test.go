package server_ctx_test

import (
	"net/http"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestRoutingSelectors(t *testing.T) {
	group := func(label string) protocol.RequestSelector {
		return protocol.RequestGroupLabelSelector{Labels: []string{label}, FromRoutingHeader: true}
	}
	ids := func(values ...string) protocol.RequestSelector {
		return protocol.RequestUpstreamIdSelector{Ids: mapset.NewThreadUnsafeSet(values...)}
	}
	tests := []struct {
		name     string
		header   http.Header
		expected []protocol.RequestSelector
	}{
		{"no headers", http.Header{"X-Custom": {"v"}}, nil},
		{"group label", http.Header{"X-Nodecore-Group-Label": {" archive "}}, []protocol.RequestSelector{group("archive")}},
		{"group label is not split", http.Header{"X-Nodecore-Group-Label": {"archive, full"}}, []protocol.RequestSelector{group("archive, full")}},
		{"repeated group label lines are joined", http.Header{"X-Nodecore-Group-Label": {"archive", "full"}}, []protocol.RequestSelector{group("archive, full")}},
		{"one id", http.Header{"X-Nodecore-Upstream-Id": {"own-archive"}}, []protocol.RequestSelector{ids("own-archive")}},
		{"comma separated ids", http.Header{"X-Nodecore-Upstream-Id": {"a, a ,  b,"}}, []protocol.RequestSelector{ids("a", "b")}},
		{"repeated id lines", http.Header{"X-Nodecore-Upstream-Id": {"a", "b,c"}}, []protocol.RequestSelector{ids("a", "b", "c")}},
		{"both headers, group first", http.Header{"X-Nodecore-Upstream-Id": {"a"}, "X-Nodecore-Group-Label": {"archive"}}, []protocol.RequestSelector{group("archive"), ids("a")}},
		{"empty values are not sent", http.Header{"X-Nodecore-Group-Label": {"  "}, "X-Nodecore-Upstream-Id": {" , ,"}}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.expected, server_ctx.RoutingSelectors(test.header.Values))
		})
	}
}

// a lowercase header line and gRPC metadata resolve like the canonical name
func TestRoutingSelectorsNameCasing(t *testing.T) {
	expected := []protocol.RequestSelector{protocol.RequestUpstreamIdSelector{Ids: mapset.NewThreadUnsafeSet("a")}}

	header := http.Header{}
	header.Add("x-nodecore-upstream-id", "a")
	assert.Equal(t, expected, server_ctx.RoutingSelectors(header.Values))

	md := metadata.Pairs("X-Nodecore-Upstream-Id", "a")
	assert.Equal(t, expected, server_ctx.RoutingSelectors(md.Get))
}
