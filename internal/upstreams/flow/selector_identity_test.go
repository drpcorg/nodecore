package flow

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestOpaquePinsDoNotShareSubscriptionSource(t *testing.T) {
	label := func(name, value string) protocol.RequestSelector {
		return protocol.RequestLabelSelector{Name: name, Values: []string{value}}
	}
	a := protocol.RequestAndSelector{Children: []protocol.RequestSelector{label("aa", "v"), label("upstream_id", "a)|label(z=x")}}
	b := protocol.RequestAndSelector{Children: []protocol.RequestSelector{label("aa", "v"), label("upstream_id", "a"), label("z", "x")}}
	body := protocol.JsonRpcRequestBody{Id: []byte(`1`), Method: "eth_subscribe", Params: []byte(`["logs",{}]`)}
	first := protocol.NewUpstreamJsonRpcRequest("1", body, false, "eth", a)
	second := protocol.NewUpstreamJsonRpcRequest("1", body, false, "eth", b)
	require.NotEqual(t, selectorKey(first.Selectors()), selectorKey(second.Selectors()))
	require.NotEqual(t, subscriptionKey(first), subscriptionKey(second))
	require.Equal(t, selectorKey([]protocol.RequestSelector{a, protocol.RequestAnySelector{}}), selectorKey([]protocol.RequestSelector{a}))
}
