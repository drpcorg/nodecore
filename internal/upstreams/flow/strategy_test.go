package flow_test

import (
	"context"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams"
	"github.com/drpcorg/dshaltie/internal/upstreams/flow"
	"github.com/drpcorg/dshaltie/internal/upstreams/fork_choice"
	"github.com/drpcorg/dshaltie/internal/upstreams/methods"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestBaseStrategyNoUpstreamsThenError(t *testing.T) {
	chSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.POLYGON, fork_choice.NewHeightForkChoice())
	baseStrategy := flow.NewBaseStrategy(chSupervisor)

	_, err := baseStrategy.SelectUpstream(nil)

	assert.NotNil(t, err)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestBaseStrategyGetUpstreams(t *testing.T) {
	chSup := createChainSupervisor()
	publishEvent(chSup, "id1", protocol.Available)
	publishEvent(chSup, "id2", protocol.Available)
	request := protocol.NewHttpUpstreamRequest("eth_getBalance", nil, nil)
	baseStrategy := flow.NewBaseStrategy(chSup)

	upId, err := baseStrategy.SelectUpstream(request)

	assert.Nil(t, err)
	assert.Equal(t, "id2", upId)

	upId, err = baseStrategy.SelectUpstream(request)

	assert.Nil(t, err)
	assert.Equal(t, "id1", upId)

	_, err = baseStrategy.SelectUpstream(request)

	assert.NotNil(t, err)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestBaseStrategyNoAvailableUpstreams(t *testing.T) {
	chSup := createChainSupervisor()
	publishEvent(chSup, "id1", protocol.Unavailable)
	baseStrategy := flow.NewBaseStrategy(chSup)
	request := protocol.NewHttpUpstreamRequest("eth_getBalance", nil, nil)

	_, err := baseStrategy.SelectUpstream(request)

	assert.NotNil(t, err)
	assert.Equal(t, protocol.NoAvailableUpstreamsError(), err)
}

func TestBaseStrategyNotSupportedMethod(t *testing.T) {
	chSup := createChainSupervisor()
	publishEvent(chSup, "id1", protocol.Unavailable)
	baseStrategy := flow.NewBaseStrategy(chSup)
	request := protocol.NewHttpUpstreamRequest("test", nil, nil)

	_, err := baseStrategy.SelectUpstream(request)

	assert.NotNil(t, err)
	assert.Equal(t, protocol.NotSupportedMethodError("test"), err)
}

func createChainSupervisor() *upstreams.ChainSupervisor {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice())

	go chainSupervisor.Start()

	return chainSupervisor
}

func publishEvent(chainSupervisor *upstreams.ChainSupervisor, upId string, status protocol.AvailabilityStatus) {
	chainSupervisor.Publish(createEvent(upId, status, 100, methods.NewEthereumLikeMethods(chains.ARBITRUM)))
	time.Sleep(5 * time.Millisecond)
}

func createEvent(id string, status protocol.AvailabilityStatus, height uint64, methods methods.Methods) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id: id,
		State: &protocol.UpstreamState{
			Status: status,
			HeadData: &protocol.BlockData{
				Height: height,
			},
			UpstreamMethods: methods,
		},
	}
}
