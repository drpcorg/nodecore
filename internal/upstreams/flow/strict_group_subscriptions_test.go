package flow

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinnedSubscriptionsDoNotUseChainWideSources(t *testing.T) {
	for _, topic := range []string{"newHeads", "logs", "newPendingTransactions", "drpc_pendingTransactions"} {
		t.Run(topic, func(t *testing.T) {
			build := func(id string) string {
				req := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["` + topic + `"]`)}, true, "eth", protocol.RequestLabelSelector{Name: protocol.UpstreamIdLabel, Values: []string{id}})
				sup := allCapsSupervisor()
				stub := sup.GetChainSupervisor(chains.ETHEREUM).(*stubChainSupervisor)
				stub.states[id] = stub.states["up1"]
				src, err := resolveSource(chains.ETHEREUM, sup, req, nil, nil, nil, allLocalSubs)
				require.NoError(t, err)
				if topic == "logs" || topic == "newPendingTransactions" {
					require.Equal(t, subscriptionKey(req), src.key, "pinned requests must not use chain-wide internal fetches")
				}
				return src.key
			}
			assert.NotEqual(t, build("a"), build("b"))
			assert.NotEqual(t, "local|"+topic, build("a"))
		})
	}
}

func TestPendingFeedNeverOpensOutsidePin(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()
	registerPendingUpstream(chSup, "allowed", pendingCaps())
	registerPendingUpstream(chSup, "outside", pendingCaps())
	ch := make(chan protocol.SubResponse, 1)
	conn := wsUpstream(t, protocol.NewJsonRpcWsUpstreamResponse(ch, "op"), nil)
	up := test_utils.TestEvmUpstream(conn, pendingTestUpConfig(), pendingMethodsMock(), nil)
	sup := mocks.NewUpstreamSupervisorMock()
	sup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	sup.On("GetUpstream", "allowed").Return(up)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newPendingTxSourceBuilder(sup, chains.ARBITRUM, protocol.RequestLabelSelector{Name: protocol.UpstreamIdLabel, Values: []string{"allowed"}})(ctx)
	require.NoError(t, err)
	defer src.Stop()
	sup.AssertNotCalled(t, "GetUpstream", "outside")
}

func TestPendingEnrichmentCannotBorrowAnotherGroupsTransaction(t *testing.T) {
	chSup, sup := enrichTestSetup(t, protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"hash":"0xaaa"}`), protocol.JsonRpc))
	tx, _ := enrichPendingTx(context.Background(), sup, chains.ARBITRUM, chSup, []byte(`"0xaaa"`), protocol.RequestLabelSelector{Name: protocol.UpstreamIdLabel, Values: []string{"other-group"}})
	assert.Nil(t, tx)
	sup.AssertNotCalled(t, "GetUpstream", "up1")
}

func TestLocalGroupFeedUsesCurrentMembership(t *testing.T) {
	for _, cap := range []protocol.Cap{protocol.NewHeadsCap} {
		sup := allCapsSupervisor()
		stub := sup.GetChainSupervisor(chains.ETHEREUM).(*stubChainSupervisor)
		state := stub.states["up1"]
		state.UpstreamMethods = methods.NewChainMethods(nil)
		group := protocol.NodeGroupID("up1", state, false)
		req := protocol.NewUpstreamJsonRpcRequest("1", protocol.JsonRpcRequestBody{Method: "eth_subscribe", Params: []byte(`["newHeads"]`)}, true, "eth", protocol.RequestLabelSelector{Name: protocol.NodeGroupLabel, Values: []string{group}})
		filter, err := localFeedFilter(cap, "test", chains.ETHEREUM, req, sup)
		require.NoError(t, err)
		require.True(t, filter("up1", state))
		moved := *state
		moved.Labels = protocol.NewLabels()
		moved.Labels.AddLabel("client_type", "changed")
		require.False(t, filter("up1", &moved), "a live feed must not follow a member after it leaves the selected group")
		stub.states["up1"] = &moved
		_, err = localFeedFilter(cap, "test", chains.ETHEREUM, req, sup)
		require.Error(t, err, "an empty group must fail instead of selecting another group")
	}
}
