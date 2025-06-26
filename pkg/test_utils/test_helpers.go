package test_utils

import (
	"context"
	json2 "encoding/json"
	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/internal/upstreams/blocks"
	specific "github.com/drpcorg/dsheltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
	"github.com/drpcorg/dsheltie/internal/upstreams/fork_choice"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/test_utils/mocks"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/stretchr/testify/mock"
	"time"
)

func GetResultAsBytes(json []byte) []byte {
	var parsed map[string]json2.RawMessage
	err := sonic.Unmarshal(json, &parsed)
	if err != nil {
		panic(err)
	}
	return parsed["result"]
}

func PolicyConfig(chain, method, connector, maxSize, ttl string, cacheEmpty bool) *config.CachePolicyConfig {
	return &config.CachePolicyConfig{
		Id:               "policy",
		Chain:            chain,
		Method:           method,
		FinalizationType: config.None,
		CacheEmpty:       cacheEmpty,
		Connector:        connector,
		ObjectMaxSize:    maxSize,
		TTL:              ttl,
	}
}

func PolicyConfigFinalized(chain, method, connector, maxSize, ttl string, cacheEmpty bool) *config.CachePolicyConfig {
	return &config.CachePolicyConfig{
		Id:               "policy",
		Chain:            chain,
		Method:           method,
		FinalizationType: config.Finalized,
		CacheEmpty:       cacheEmpty,
		Connector:        connector,
		ObjectMaxSize:    maxSize,
		TTL:              ttl,
	}
}

func CreateEvent(id string, status protocol.AvailabilityStatus, height uint64, methods methods.Methods) protocol.UpstreamEvent {
	return CreateEventWithBlockData(id, status, height, methods, nil)
}

func CreateEventWithBlockData(
	id string,
	status protocol.AvailabilityStatus,
	height uint64,
	methods methods.Methods,
	blockInfo *protocol.BlockInfo,
) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id: id,
		State: &protocol.UpstreamState{
			Status: status,
			HeadData: &protocol.BlockData{
				Height: height,
			},
			UpstreamMethods: methods,
			BlockInfo:       blockInfo,
		},
	}
}

func GetMethodMockAndUpSupervisor() (*mocks.MethodsMock, *mocks.UpstreamSupervisorMock) {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.POLYGON, fork_choice.NewHeightForkChoice(), nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetMethod", mock.Anything).Return(specs.DefaultMethod("name"))
	methodsMock.On("HasMethod", mock.Anything).Return(true)
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_superTest"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(CreateEvent("id", protocol.Available, 100, methodsMock))
	time.Sleep(20 * time.Millisecond)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(chainSupervisor)

	return methodsMock, upSupervisor
}

func TestUpstream(ctx context.Context, connector connectors.ApiConnector, upConfig *config.Upstream) *upstreams.Upstream {
	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(protocol.UpstreamState{Status: protocol.Available})

	return upstreams.NewUpstreamWithParams(
		context.Background(),
		"id",
		chains.ETHEREUM,
		[]connectors.ApiConnector{connector},
		blocks.NewHeadProcessor(ctx, upConfig, connector, specific.EvmChainSpecific),
		upState,
	)
}

func CreateChainSupervisor() *upstreams.ChainSupervisor {
	chainSupervisor := upstreams.NewChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil)

	go chainSupervisor.Start()

	return chainSupervisor
}

func PublishEvent(chainSupervisor *upstreams.ChainSupervisor, upId string, status protocol.AvailabilityStatus, caps mapset.Set[protocol.Cap]) {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_getBalance"))
	methodsMock.On("HasMethod", "eth_getBalance").Return(true)
	methodsMock.On("HasMethod", "test").Return(false)
	chainSupervisor.Publish(createEvent(upId, status, 100, methodsMock, caps))
	time.Sleep(10 * time.Millisecond)
}

func createEvent(id string, status protocol.AvailabilityStatus, height uint64, methods methods.Methods, caps mapset.Set[protocol.Cap]) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id: id,
		State: &protocol.UpstreamState{
			Status: status,
			HeadData: &protocol.BlockData{
				Height: height,
			},
			UpstreamMethods: methods,
			Caps:            caps,
		},
	}
}
