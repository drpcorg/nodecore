package upstreams_test

import (
	"context"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams"
	"github.com/drpcorg/dshaltie/internal/upstreams/fork_choice"
	"github.com/drpcorg/dshaltie/internal/upstreams/methods"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

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

func TestChainSupervisorUpdateHeadWithHeightFc(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice())

	go chainSupervisor.Start()

	chainSupervisor.Publish(createEvent("id", protocol.Available, 100, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(100), chainSupervisor.GetChainState().Head)

	chainSupervisor.Publish(createEvent("id1", protocol.Available, 95, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(100), chainSupervisor.GetChainState().Head)

	chainSupervisor.Publish(createEvent("id3", protocol.Unavailable, 500, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(100), chainSupervisor.GetChainState().Head)

	chainSupervisor.Publish(createEvent("id", protocol.Available, 1000, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, uint64(1000), chainSupervisor.GetChainState().Head)
}

func TestChainSupervisorUpdateStatus(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice())

	go chainSupervisor.Start()

	chainSupervisor.Publish(createEvent("id", protocol.Available, 100, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(createEvent("id1", protocol.Unavailable, 95, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(createEvent("id", protocol.Unavailable, 500, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, protocol.Unavailable, chainSupervisor.GetChainState().Status)

	chainSupervisor.Publish(createEvent("id12", protocol.Available, 95, methods.NewSolanaMethods()))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, protocol.Available, chainSupervisor.GetChainState().Status)
}

func TestChainSupervisorUnionUpstreamMethods(t *testing.T) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice())
	config1 := &config.MethodsConfig{EnableMethods: []string{"test1"}, DisableMethods: []string{"default"}}
	methods1 := methods.NewUpstreamMethods(methods.NewEthereumLikeMethods(chains.ARBITRUM), config1)
	config2 := &config.MethodsConfig{EnableMethods: []string{"test2"}, DisableMethods: []string{"default"}}
	methods2 := methods.NewUpstreamMethods(methods.NewEthereumLikeMethods(chains.ARBITRUM), config2)
	config3 := &config.MethodsConfig{EnableMethods: []string{"test2", "test5"}, DisableMethods: []string{"default"}}
	methods3 := methods.NewUpstreamMethods(methods.NewEthereumLikeMethods(chains.ARBITRUM), config3)

	go chainSupervisor.Start()

	chainSupervisor.Publish(createEvent("id", protocol.Available, 100, methods1))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(createEvent("id2", protocol.Available, 100, methods2))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1", "test2"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())

	chainSupervisor.Publish(createEvent("id1", protocol.Available, 100, methods3))
	time.Sleep(3 * time.Millisecond)
	assert.Equal(t, mapset.NewThreadUnsafeSet[string]("test1", "test2", "test5"), chainSupervisor.GetChainState().Methods.GetSupportedMethods())
}
