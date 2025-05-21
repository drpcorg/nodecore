package upstreams

import (
	"context"
	"fmt"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/blocks"
	specific "github.com/drpcorg/dsheltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/test_utils"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
	"time"
)

func TestUpstreamEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connector := test_utils.NewHttpConnectorMock()
	body := []byte(`{
	  "jsonrpc": "2.0",
	  "result": {
		"number": "0x41fd60b",
		"hash": "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18"
	  }
	}`)
	response := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)
	connector.On("SendRequest", mock.Anything, mock.Anything).Return(response)

	upConfig := &config.Upstream{
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
	}

	upstream := testUpstream(ctx, connector, upConfig)
	go upstream.Start()

	sub := upstream.Subscribe("name")

	event, ok := <-sub.Events
	state := protocol.UpstreamState{
		Status: protocol.Available,
		HeadData: &protocol.BlockData{
			Height: uint64(69195275),
			Hash:   "0xdeeaae5f33e2a990aab15d48c26118fd8875f1a2aaac376047268d80f2486d18",
		},
	}
	expected := protocol.UpstreamEvent{
		Id:    "id",
		Chain: chains.ETHEREUM,
		State: &state,
	}

	connector.AssertExpectations(t)
	assert.True(t, ok)
	assert.Equal(t, expected, event)
	assert.Equal(t, state, upstream.GetUpstreamState())
}

func testUpstream(ctx context.Context, connector connectors.ApiConnector, upConfig *config.Upstream) *Upstream {
	ctx, cancel := context.WithCancel(ctx)
	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(protocol.UpstreamState{Status: protocol.Available})

	return &Upstream{
		Id:            "id",
		Chain:         chains.ETHEREUM,
		apiConnectors: []connectors.ApiConnector{connector},
		ctx:           ctx,
		cancelFunc:    cancel,
		upstreamState: upState,
		headProcessor: blocks.NewHeadProcessor(ctx, upConfig, connector, specific.EvmChainSpecific),
		subManager:    utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", "id")),
		stateChan:     make(chan protocol.AbstractUpstreamStateEvent, 100),
	}
}
