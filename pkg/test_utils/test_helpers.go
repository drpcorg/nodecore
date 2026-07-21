package test_utils

import (
	"context"
	json2 "encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/resilience"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/algorand_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/aptos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/aztec_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/beacon_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/bitcoin_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific/solana_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/fork_choice"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/mock"
)

func ExpectEthValidationRequest(
	connector *mocks.ConnectorMock,
	request protocol.RequestHolder,
	response protocol.ResponseHolder,
) {
	call := connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(UpstreamJsonRpcRequestMatcher(request))).
		Return(response)

	if response.HasError() {
		call.Times(validations.RetryMaxAttempts)
		return
	}

	call.Once()
}

func BuildLocalKeyConfig(key string, allowedIps []string, methods *config.AuthMethods, contracts *config.AuthContracts) *config.LocalKeyConfig {
	return &config.LocalKeyConfig{
		Key: key,
		KeySettingsConfig: &config.KeySettingsConfig{
			AllowedIps:    allowedIps,
			Methods:       methods,
			AuthContracts: contracts,
		},
	}
}

func NewUpstreamRequest(t *testing.T, method string, params any) protocol.RequestHolder {
	t.Helper()
	req, err := protocol.NewInternalUpstreamJsonRpcRequest(method, params, chains.ETHEREUM)
	if err != nil {
		t.Fatalf("failed to build UpstreamJsonRpcRequest: %v", err)
	}
	return req
}

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

func PolicyConfigBlockchainType(blockchainType, method, connector, maxSize, ttl string, cacheEmpty bool) *config.CachePolicyConfig {
	return &config.CachePolicyConfig{
		Id:               "policy",
		BlockchainType:   blockchainType,
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

func CreateEvent(id string, status protocol.AvailabilityStatus, head protocol.Block, methods methods.Methods) protocol.UpstreamEvent {
	return CreateEventWithBlockData(id, status, head, methods, nil)
}

func CreateRemoveEvent(id string) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id:        id,
		EventType: &protocol.RemoveUpstreamEvent{},
	}
}

func CreateEventWithBlockData(
	id string,
	status protocol.AvailabilityStatus,
	head protocol.Block,
	methods methods.Methods,
	blockInfo *protocol.BlockInfo,
) protocol.UpstreamEvent {
	state := protocol.DefaultUpstreamState(
		methods,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		"",
		nil,
		nil,
	)
	state.Status = status
	state.HeadData = head
	state.BlockInfo = blockInfo

	return protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.StateUpstreamEvent{
			State: &state,
		},
	}
}

func GetMethodMockAndUpSupervisor() (*mocks.MethodsMock, *mocks.UpstreamSupervisorMock) {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.POLYGON, fork_choice.NewHeightForkChoice(), nil, false, nil)
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_superTest"))

	go chainSupervisor.Start()

	chainSupervisor.PublishUpstreamEvent(CreateEvent("id", protocol.Available, protocol.NewBlockWithHeight(100), methodsMock))
	time.Sleep(20 * time.Millisecond)

	upSupervisor := mocks.NewUpstreamSupervisorMock()
	upSupervisor.On("GetChainSupervisor", mock.Anything).Return(chainSupervisor)

	return methodsMock, upSupervisor
}

func TestEvmUpstream(
	connector connectors.ApiConnector,
	upConfig *config.Upstream,
	upstreamMethods methods.Methods,
	processorAggregator *event_processors.UpstreamProcessorAggregator,
) *upstreams.BaseUpstream {
	index := "00012"
	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(
		protocol.DefaultUpstreamState(
			upstreamMethods,
			mapset.NewThreadUnsafeSet[protocol.Cap](),
			"00012",
			nil,
			nil,
		),
	)

	return upstreams.NewBaseUpstreamWithParams(
		"id",
		chains.ETHEREUM,
		[]connectors.ApiConnector{connector},
		upConfig,
		index,
		upState,
		processorAggregator,
		nil,
		nil,
	)
}

func NewEvmChainSpecific(connector connectors.ApiConnector) *evm_specific.EvmChainSpecificObject {
	return evm_specific.NewEvmChainSpecific(
		context.Background(),
		"id",
		connector,
		nil,
		chains.GetChain("polygon"),
		1*time.Second,
		newTestChainOptions(),
	)
}

func NewSolanaChainSpecific(ctx context.Context, connector connectors.ApiConnector) *specific.SolanaChainSpecificObject {
	return specific.NewSolanaChainSpecificObject(ctx, chains.GetChain("solana"), "id", connector, newTestChainOptions())
}

func NewAztecChainSpecific(ctx context.Context, connector connectors.ApiConnector) *aztec_specific.AztecChainSpecificObject {
	options := &chains.Options{
		InternalTimeout:         5 * time.Second,
		ValidationInterval:      10 * time.Second,
		DisableChainValidation:  new(false),
		DisableHealthValidation: new(false),
	}
	return aztec_specific.NewAztecChainSpecificObject(ctx, chains.GetChain("aztec-mainnet"), "id", options, connector)
}

func NewAlgorandChainSpecific(ctx context.Context, connector connectors.ApiConnector) *algorand_specific.AlgorandChainSpecificObject {
	options := &chains.Options{
		InternalTimeout:         5 * time.Second,
		ValidationInterval:      10 * time.Second,
		DisableChainValidation:  new(false),
		DisableHealthValidation: new(false),
	}
	return algorand_specific.NewAlgorandChainSpecificObject(ctx, chains.GetChain("algorand-mainnet"), "id", connector, options)
}

func NewBeaconChainSpecific(ctx context.Context, connector connectors.ApiConnector) *beacon_specific.BeaconChainSpecificObject {
	options := &chains.Options{
		InternalTimeout:         5 * time.Second,
		ValidationInterval:      10 * time.Second,
		DisableChainValidation:  new(false),
		DisableHealthValidation: new(false),
	}
	return beacon_specific.NewBeaconChainSpecificObject(ctx, chains.GetChain("eth-beacon-chain"), "id", connector, time.Hour, options)
}

func NewAptosChainSpecific(ctx context.Context, connector connectors.ApiConnector) *aptos_specific.AptosChainSpecificObject {
	return aptos_specific.NewAptosChainSpecificObject(ctx, chains.GetChain("aptos-mainnet"), "id", connector, newTestChainOptions())
}

func NewBitcoinChainSpecific(ctx context.Context, connector connectors.ApiConnector) *bitcoin_specific.BitcoinChainSpecificObject {
	options := &chains.Options{
		InternalTimeout:         5 * time.Second,
		ValidationInterval:      10 * time.Second,
		DisableChainValidation:  new(false),
		DisableHealthValidation: new(false),
	}
	return bitcoin_specific.NewBitcoinChainSpecificObject(ctx, chains.GetChain("bitcoin-mainnet"), "id", connector, options)
}

func newTestChainOptions() *chains.Options {
	return &chains.Options{
		InternalTimeout:             5 * time.Second,
		ValidationInterval:          10 * time.Second,
		DisableValidation:           new(false),
		DisableSettingsValidation:   new(false),
		DisableChainValidation:      new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(false),
		DisableLabelsDetection:      new(false),
		ValidateSyncing:             new(false),
		ValidatePeers:               new(false),
	}
}

func CreateChainSupervisor() upstreams.ChainSupervisor {
	chainSupervisor := upstreams.NewBaseChainSupervisor(context.Background(), chains.ARBITRUM, fork_choice.NewHeightForkChoice(), nil, false, nil)

	go chainSupervisor.Start()

	return chainSupervisor
}

func PublishEvent(chainSupervisor upstreams.ChainSupervisor, upId string, status protocol.AvailabilityStatus, caps mapset.Set[protocol.Cap]) {
	methodsMock := mocks.NewMethodsMock()
	methodsMock.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_getBalance"))
	methodsMock.On("HasMethod", "eth_getBalance").Return(true)
	methodsMock.On("HasMethod", "test").Return(false)
	chainSupervisor.PublishUpstreamEvent(createEvent(upId, status, 100, methodsMock, caps, "index"))
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
	state := protocol.DefaultUpstreamState(
		methods,
		caps,
		upstreamIndex,
		nil,
		nil,
	)
	state.Status = status
	state.HeadData = protocol.Block{Height: height}

	return protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.StateUpstreamEvent{
			State: &state,
		},
	}
}
