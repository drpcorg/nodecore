package test_utils

import (
	"context"
	json2 "encoding/json"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/mock"
)

func CtxWithRemoteAddr(remote string) context.Context {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.RemoteAddr = remote
	return utils.ContextWithIps(context.Background(), req)
}

func CtxWithXFF(xff string) context.Context {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	return utils.ContextWithIps(context.Background(), req)
}

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
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_superTest"))

	go chainSupervisor.Start()

	chainSupervisor.Publish(CreateEvent("id", protocol.Available, 100, methodsMock))
	time.Sleep(20 * time.Millisecond)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(chainSupervisor)

	return methodsMock, upSupervisor
}

func TestEvmUpstream(
	ctx context.Context,
	connector connectors.ApiConnector,
	upConfig *config.Upstream,
	blockProcessor blocks.BlockProcessor,
	upstreamMethods methods.Methods,
) *upstreams.Upstream {
	index := "00012"
	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(
		protocol.DefaultUpstreamState(
			upstreamMethods,
			mapset.NewThreadUnsafeSet[protocol.Cap](),
			"00012",
			nil,
		),
	)

	return upstreams.NewUpstreamWithParams(
		context.Background(),
		"id",
		chains.ETHEREUM,
		[]connectors.ApiConnector{connector},
		blocks.NewHeadProcessor(ctx, upConfig, connector, specific.EvmChainSpecific),
		blockProcessor,
		upState,
		index,
		upConfig.Methods,
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
	chainSupervisor.Publish(createEvent(upId, status, 100, methodsMock, caps, "index"))
	time.Sleep(10 * time.Millisecond)
}

func CreateExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return resilience.CreateFlowExecutor()
}

func createEvent(
	id string,
	status protocol.AvailabilityStatus,
	height uint64,
	methods methods.Methods,
	caps mapset.Set[protocol.Cap],
	upstreamIndex string,
) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id: id,
		State: &protocol.UpstreamState{
			Status: status,
			HeadData: &protocol.BlockData{
				Height: height,
			},
			UpstreamMethods: methods,
			Caps:            caps,
			UpstreamIndex:   upstreamIndex,
		},
	}
}
