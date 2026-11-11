package upstreams

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/stats/hook"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/algorand_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/aptos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/aztec_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/beacon_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/bitcoin_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/celestia_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/cosmos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/near_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/polkadot_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/solana_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/starknet_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/stellar_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/sui_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/ton_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tron_specific"
	"github.com/drpcorg/public/pkg/methods"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type UpstreamStatsService interface {
	AddRequestResults(requestResults []protocol.RequestResult)
}

type upstreamCreationData struct {
	upstreamConnectorsInfo *connectorsInfo
	upstreamMethods        *methods.UpstreamMethods
	rt                     *ratelimiter.RateLimitBudget
	autoTune               *ratelimiter.UpstreamAutoTune
	// compatibleClients is the process-wide client-version rule holder, nil when the
	// compatible-clients check is disabled
	compatibleClients *compatible_clients.Registry
}

// UpstreamFactory builds an upstream from its effective config. The arguments are what
// differs from one upstream to another; everything shared by all of them belongs to the
// implementation.
type UpstreamFactory interface {
	Create(
		ctx context.Context,
		conf *config.Upstream,
		executor failsafe.Executor[protocol.ResponseHolder],
		upstreamIndex int,
	) (Upstream, error)
}

// GenericUpstreamFactory holds the process-wide collaborators every upstream is built with.
type GenericUpstreamFactory struct {
	tracker                 dimensions.DimensionTracker
	statsService            UpstreamStatsService
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry
	torProxyUrl             string
	// compatibleClients is nil when the compatible-clients check is disabled
	compatibleClients *compatible_clients.Registry
}

var _ UpstreamFactory = (*GenericUpstreamFactory)(nil)

func NewGenericUpstreamFactory(
	tracker dimensions.DimensionTracker,
	statsService UpstreamStatsService,
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
	torProxyUrl string,
	compatibleClients *compatible_clients.Registry,
) *GenericUpstreamFactory {
	return &GenericUpstreamFactory{
		tracker:                 tracker,
		statsService:            statsService,
		rateLimitBudgetRegistry: rateLimitBudgetRegistry,
		torProxyUrl:             torProxyUrl,
		compatibleClients:       compatibleClients,
	}
}

func (f *GenericUpstreamFactory) Create(
	ctx context.Context,
	conf *config.Upstream,
	executor failsafe.Executor[protocol.ResponseHolder],
	upstreamIndex int,
) (Upstream, error) {
	ctx, cancel := context.WithCancel(ctx)
	configuredChain := chains.GetChain(conf.ChainName)

	upstreamConnectorsInfo, err := f.createUpstreamConnectors(ctx, conf, configuredChain, executor)
	if err != nil {
		cancel()
		return nil, err
	}

	upstreamMethods, err := methods.NewUpstreamMethods(configuredChain.MethodSpec, conf.Methods, conf.GetApiConnectorTypes())
	if err != nil {
		cancel()
		return nil, err
	}

	rt, autoTune := f.createRateLimiter(ctx, conf)

	creationData := &upstreamCreationData{
		upstreamConnectorsInfo: upstreamConnectorsInfo,
		upstreamMethods:        upstreamMethods,
		rt:                     rt,
		autoTune:               autoTune,
		compatibleClients:      f.compatibleClients,
	}

	up, err := NewGenericUpstream(ctx, cancel, conf, configuredChain, upstreamIndex, creationData)
	if err != nil {
		// releases what is already bound to the context: the ws request registry loop and the
		// rate-limit auto-tuner
		cancel()
		return nil, err
	}
	return up, nil
}

func (f *GenericUpstreamFactory) createRateLimiter(
	ctx context.Context,
	conf *config.Upstream,
) (*ratelimiter.RateLimitBudget, *ratelimiter.UpstreamAutoTune) {
	var rt *ratelimiter.RateLimitBudget
	if conf.RateLimit != nil {
		rt = ratelimiter.NewRateLimitBudget(&config.RateLimitBudget{
			Name:   "inplace",
			Config: conf.RateLimit,
		}, ratelimiter.NewRateLimitMemoryEngine())
	} else if conf.RateLimitBudget != "" {
		rateLimitBudget, ok := f.rateLimitBudgetRegistry.Get(conf.RateLimitBudget)
		if !ok {
			log.Panic().Msgf("rate limit budget %s not found", conf.RateLimitBudget)
		}
		rt = rateLimitBudget
	}
	var autoTuneRateLimiter *ratelimiter.UpstreamAutoTune
	if conf.RateLimitAutoTune != nil && conf.RateLimitAutoTune.Enabled {
		autoTuneRateLimiter = ratelimiter.NewUpstreamAutoTune(ctx, conf.Id, conf.RateLimitAutoTune)
	}
	return rt, autoTuneRateLimiter
}

func (f *GenericUpstreamFactory) createUpstreamConnectors(
	ctx context.Context,
	conf *config.Upstream,
	configuredChain *chains.ConfiguredChain,
	executor failsafe.Executor[protocol.ResponseHolder],
) (*connectorsInfo, error) {
	apiConnectors := make([]connectors.ApiConnector, 0)
	var headConnector connectors.ApiConnector
	var internalRequestConnector connectors.ApiConnector

	for _, connectorConfig := range conf.Connectors {
		apiConnector, err := f.createConnector(ctx, conf.Id, configuredChain, connectorConfig)
		if err != nil {
			return nil, fmt.Errorf("couldn't create api connector of %s: %v", conf.Id, err)
		}
		hooks := []protocol.ResponseReceivedHook{
			dimensions.NewDimensionHook(f.tracker),
			hook.NewStatsHook(f.statsService),
		}
		apiConnector = connectors.NewObserverConnector(configuredChain.Chain, conf.Id, apiConnector, hooks, executor)
		if connectorConfig.GetApiConnectorType() == conf.GetHeadApiConnectorType() {
			headConnector = apiConnector
		}
		if connectorConfig.GetApiConnectorType() == conf.GetBestConnector(config.DefaultMode) {
			internalRequestConnector = apiConnector
		}
		apiConnectors = append(apiConnectors, apiConnector)
	}

	return newConnectorInfo(headConnector, internalRequestConnector, apiConnectors), nil
}

func (f *GenericUpstreamFactory) createConnector(
	ctx context.Context,
	upId string,
	configuredChain *chains.ConfiguredChain,
	connectorConfig *config.ApiConnectorConfig,
) (connectors.ApiConnector, error) {
	torProxyUrl := f.torProxyUrl
	switch connectorConfig.GetApiConnectorType() {
	case specs.JsonRpcConnector:
		return connectors.NewHttpConnector(connectorConfig, specs.JsonRpcConnector, torProxyUrl, upId)
	case specs.WebsocketConnector:
		wsProtocol := newWsProtocol(upId, configuredChain)
		dialWsService := ws.NewDefaultDialWsService(connectorConfig, torProxyUrl)
		reqRegistry := ws.NewGenericRequestRegistry(ctx, configuredChain.Chain, upId, configuredChain.MethodSpec)
		wsProcessor, err := ws.NewGenericWsProcessor(
			ctx,
			upId,
			connectorConfig.Url,
			dialWsService,
			reqRegistry,
			ws.NewWebsocketSession(),
			wsProtocol,
		)
		if err != nil {
			return nil, err
		}
		return connectors.NewWsConnector(wsProcessor), nil
	case specs.TendermintConnector:
		return connectors.NewHttpConnector(connectorConfig, specs.TendermintConnector, torProxyUrl, upId)
	case specs.RestConnector:
		return connectors.NewHttpConnector(connectorConfig, specs.RestConnector, torProxyUrl, upId)
	case specs.RestIndexer:
		return connectors.NewHttpConnector(connectorConfig, specs.RestIndexer, torProxyUrl, upId)
	case specs.RestAdditional:
		return connectors.NewHttpConnector(connectorConfig, specs.RestAdditional, torProxyUrl, upId)
	case specs.GrpcConnector:
		return connectors.NewGrpcConnector(connectorConfig, specs.GrpcConnector, upId)
	case specs.GrpcAdditional:
		return connectors.NewGrpcConnector(connectorConfig, specs.GrpcAdditional, upId)
	default:
		panic(fmt.Sprintf("unknown connector type - %s", connectorConfig.Type))
	}
}

func createLowerBoundsProcessor(
	ctx context.Context,
	chainSpecific chains_specific.ChainSpecific,
	conf *config.Upstream,
) lower_bounds.LowerBoundProcessor {
	var detected lower_bounds.LowerBoundProcessor
	if !*conf.Options.DisableLowerBoundsDetection {
		detected = chainSpecific.LowerBoundProcessor()
	}
	manual := manualLowerBounds(conf.LowerBounds)
	if len(manual) == 0 {
		return detected
	}
	// every chain family builds a GenericLowerBoundProcessor; nil means no detection
	generic, ok := detected.(*lower_bounds.GenericLowerBoundProcessor)
	if detected != nil && !ok {
		log.Panic().Msgf("upstream '%s': lower-bounds needs a *GenericLowerBoundProcessor, got %T", conf.Id, detected)
	}
	return lower_bounds.WithManualBounds(ctx, conf.Id, manual, generic)
}

func manualLowerBounds(bounds config.UpstreamLowerBounds) map[protocol.LowerBoundType]int64 {
	if len(bounds) == 0 {
		return nil
	}
	manual := make(map[protocol.LowerBoundType]int64, len(bounds))
	for name, bound := range bounds {
		boundType, ok := protocol.ParseLowerBoundType(name)
		if !ok {
			log.Panic().Msgf("unknown lower bound type '%s'", name)
		}
		manual[boundType] = bound
	}
	return manual
}

// createSettingValidationProcessor combines the family's own settings validators with
// the client-version validator, which every family with a client detector gets when
// the compatible-clients rules are enabled (a nil holder means disabled).
func createSettingValidationProcessor(
	chainSpecific chains_specific.ChainSpecific,
	conf *config.Upstream,
	configuredChain *chains.ConfiguredChain,
	compatibleClients *compatible_clients.Registry,
) *validations.ValidationProcessor[validations.ValidationSettingResult] {
	if *conf.Options.DisableValidation || *conf.Options.DisableSettingsValidation {
		return nil
	}

	validators := chainSpecific.SettingsValidators()
	if compatibleClients != nil {
		if detector := chainSpecific.ClientLabelsDetector(); detector != nil {
			validators = append(validators, validations.NewClientVersionValidator(conf.Id, configuredChain, detector, compatibleClients))
		}
	}
	if len(validators) == 0 {
		return nil
	}
	return validations.NewSettingsValidationProcessor(validators)
}

func createHealthValidationProcessor(chainSpecific chains_specific.ChainSpecific, options *chains.Options) *validations.ValidationProcessor[protocol.AvailabilityStatus] {
	if *options.DisableValidation || *options.DisableHealthValidation {
		return nil
	}
	validators := chainSpecific.HealthValidators()
	if len(validators) == 0 {
		return nil
	}
	return validations.NewHealthValidationProcessor(validators)
}

func createLabelsProcessor(chainSpecific chains_specific.ChainSpecific, options *chains.Options) labels.LabelsProcessor {
	if *options.DisableLabelsDetection {
		return nil
	}
	return chainSpecific.LabelsProcessor()
}

func createMethodsProcessor(chainSpecific chains_specific.ChainSpecific, options *chains.Options) methods.MethodsProcessor {
	if *options.DisableMethodsDetection {
		return nil
	}
	return chainSpecific.MethodsProcessor()
}

func createBlockProcessor(chainSpecific chains_specific.ChainSpecific) blocks.BlockProcessor {
	return chainSpecific.BlockProcessor()
}

// upstreamSpecifics are the two roles a chain specific plays. The probe
// specific is built from the internal-request connector and runs every poll:
// validators, labels, lower bounds, method detection and the finalized/safe
// block processor, and answers PauseHeadWhileSyncing, which its own syncing
// validator drives. The head specific is built from the head connector and
// drives only the head processor - the one place that connector's protocol
// matters, since a head subscription is opened on it. On the usual
// upstream both connectors are the same and so is the specific. On a
// mixed-API upstream (an Injective node with json-rpc probes and a grpc head)
// they belong to different API families, and each is built for the connector
// it actually talks to - a head driven by the probe specific would try to open
// eth_subscribe over gRPC.
type upstreamSpecifics struct {
	probe chains_specific.ChainSpecific
	head  chains_specific.ChainSpecific
}

func getUpstreamSpecifics(
	ctx context.Context,
	conf *config.Upstream,
	upstreamConnectorsInfo *connectorsInfo,
	configuredChain *chains.ConfiguredChain,
) (*upstreamSpecifics, error) {
	probe, err := getChainSpecific(ctx, conf, upstreamConnectorsInfo.internalRequestConnector, upstreamConnectorsInfo.allConnectors, configuredChain)
	if err != nil {
		return nil, err
	}
	head := probe
	if upstreamConnectorsInfo.headConnector != upstreamConnectorsInfo.internalRequestConnector {
		head, err = getChainSpecific(ctx, conf, upstreamConnectorsInfo.headConnector, upstreamConnectorsInfo.allConnectors, configuredChain)
		if err != nil {
			return nil, err
		}
	}
	return &upstreamSpecifics{probe: probe, head: head}, nil
}

// getChainSpecific builds the chain's specific for one connector: the chain
// family picks the implementation, and on chains served by several API
// families the connector's type picks the flavor.
func getChainSpecific(
	ctx context.Context,
	conf *config.Upstream,
	connector connectors.ApiConnector,
	allConnectors []connectors.ApiConnector,
	configuredChain *chains.ConfiguredChain,
) (chains_specific.ChainSpecific, error) {
	switch configuredChain.Type {
	case chains.Ethereum:
		if chains.IsTron(configuredChain.Chain) {
			return tron_specific.NewTronSpecific(
				ctx,
				conf.Id,
				connector,
				configuredChain,
				conf.PollInterval,
				conf.Options,
				conf.Labels,
			)
		}
		return evm_specific.NewEvmChainSpecific(
			ctx,
			conf.Id,
			connector,
			allConnectors,
			configuredChain,
			conf.PollInterval,
			conf.Options,
			conf.Labels,
		), nil
	case chains.Aztec:
		return aztec_specific.NewAztecChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			conf.Options,
			connector,
		), nil
	case chains.Algorand:
		return algorand_specific.NewAlgorandChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.Options,
		), nil
	case chains.Bitcoin:
		return bitcoin_specific.NewBitcoinChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.Options,
		), nil
	case chains.EthereumBeaconChain:
		return beacon_specific.NewBeaconChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Aptos:
		return aptos_specific.NewAptosChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.Options,
		), nil
	case chains.Near:
		return near_specific.NewNearChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Solana:
		return solana_specific.NewSolanaChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			allConnectors,
			conf.Options,
		), nil
	case chains.Ton:
		return ton_specific.NewTonChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			allConnectors,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Cosmos:
		return cosmos_specific.NewCosmosSpecific(
			ctx,
			conf.Id,
			connector,
			allConnectors,
			configuredChain,
			conf.PollInterval,
			conf.Options,
			conf.Labels,
		)
	case chains.Starknet:
		return starknet_specific.NewStarknetChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Stellar:
		return stellar_specific.NewStellarChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Sui:
		return sui_specific.NewSuiChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Polkadot:
		return polkadot_specific.NewPolkadotChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			connector,
			conf.Options,
		), nil
	case chains.Celestia:
		return celestia_specific.NewCelestiaSpecific(
			ctx,
			conf.Id,
			connector,
			configuredChain,
			conf.PollInterval,
			conf.Options,
		)
	default:
		panic(fmt.Sprintf("unknown blockchain type - %s", configuredChain.Type))
	}
}

func getUpstreamVendor(connectors []*config.ApiConnectorConfig) UpstreamVendor {
	urls := lo.Map(connectors, func(item *config.ApiConnectorConfig, index int) string {
		return item.Url
	})
	return DetectUpstreamVendor(urls)
}

type connectorsInfo struct {
	headConnector            connectors.ApiConnector
	internalRequestConnector connectors.ApiConnector
	allConnectors            []connectors.ApiConnector
}

func newConnectorInfo(
	headConnector,
	internalRequestConnector connectors.ApiConnector,
	allConnectors []connectors.ApiConnector,
) *connectorsInfo {
	return &connectorsInfo{
		headConnector:            headConnector,
		internalRequestConnector: internalRequestConnector,
		allConnectors:            allConnectors,
	}
}

// newWsProtocol picks the subscription dialect an upstream websocket speaks:
// go-jsonrpc channels for a celestia DA node, the JSON-RPC subscription model
// (eth_subscribe and the like) everywhere else.
func newWsProtocol(upId string, configuredChain *chains.ConfiguredChain) ws.WsProtocol {
	if configuredChain.Type == chains.Celestia {
		return ws.NewChannelWsProtocol(upId, configuredChain.MethodSpec)
	}
	return ws.NewJsonRpcWsProtocol(upId, configuredChain.MethodSpec, configuredChain.Chain)
}
