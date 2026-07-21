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
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/near_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/ripple_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/solana_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/starknet_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tron_specific"
	"github.com/drpcorg/nodecore/pkg/methods"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
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
}

func CreateUpstream(
	ctx context.Context,
	conf *config.Upstream,
	tracker dimensions.DimensionTracker,
	statsService UpstreamStatsService,
	executor failsafe.Executor[protocol.ResponseHolder],
	upstreamIndex int,
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
	torProxyUrl string,
) (Upstream, error) {
	ctx, cancel := context.WithCancel(ctx)
	configuredChain := chains.GetChain(conf.ChainName)

	upstreamConnectorsInfo, err := createUpstreamConnectors(ctx, conf, configuredChain, tracker, statsService, executor, torProxyUrl)
	if err != nil {
		cancel()
		return nil, err
	}

	upstreamMethods, err := methods.NewUpstreamMethods(configuredChain.MethodSpec, conf.Methods, conf.GetApiConnectorTypes())
	if err != nil {
		cancel()
		return nil, err
	}

	rt, autoTune := createRateLimiter(ctx, conf, rateLimitBudgetRegistry)

	creationData := &upstreamCreationData{
		upstreamConnectorsInfo: upstreamConnectorsInfo,
		upstreamMethods:        upstreamMethods,
		rt:                     rt,
		autoTune:               autoTune,
	}

	return NewBaseUpstream(ctx, cancel, conf, configuredChain, upstreamIndex, creationData)
}

func createRateLimiter(
	ctx context.Context,
	conf *config.Upstream,
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
) (*ratelimiter.RateLimitBudget, *ratelimiter.UpstreamAutoTune) {
	var rt *ratelimiter.RateLimitBudget
	if conf.RateLimit != nil {
		rt = ratelimiter.NewRateLimitBudget(&config.RateLimitBudget{
			Name:   "inplace",
			Config: conf.RateLimit,
		}, ratelimiter.NewRateLimitMemoryEngine())
	} else if conf.RateLimitBudget != "" {
		rateLimitBudget, ok := rateLimitBudgetRegistry.Get(conf.RateLimitBudget)
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

func createLowerBoundsProcessor(chainSpecific chains_specific.ChainSpecific, options *chains.Options) lower_bounds.LowerBoundProcessor {
	if *options.DisableLowerBoundsDetection {
		return nil
	}
	return chainSpecific.LowerBoundProcessor()
}

func createConnector(
	ctx context.Context,
	upId string,
	configuredChain *chains.ConfiguredChain,
	connectorConfig *config.ApiConnectorConfig,
	torProxyUrl string,
) (connectors.ApiConnector, error) {
	switch connectorConfig.GetApiConnectorType() {
	case specs.JsonRpcConnector:
		return connectors.NewHttpConnector(connectorConfig, specs.JsonRpcConnector, torProxyUrl, upId)
	case specs.WebsocketConnector:
		jsonRpcWsProtocol := ws.NewJsonRpcWsProtocol(upId, configuredChain.MethodSpec, configuredChain.Chain)
		dialWsService := ws.NewDefaultDialWsService(connectorConfig, torProxyUrl)
		reqRegistry := ws.NewBaseRequestRegistry(ctx, configuredChain.Chain, upId, configuredChain.MethodSpec)
		wsProcessor, err := ws.NewBaseWsProcessor(
			ctx,
			upId,
			connectorConfig.Url,
			dialWsService,
			reqRegistry,
			ws.NewWebsocketSession(),
			jsonRpcWsProtocol,
		)
		if err != nil {
			return nil, err
		}
		return connectors.NewWsConnector(wsProcessor), nil
	case specs.RestConnector:
		return connectors.NewHttpConnector(connectorConfig, specs.RestConnector, torProxyUrl, upId)
	case specs.RestAdditional:
		return connectors.NewHttpConnector(connectorConfig, specs.RestAdditional, torProxyUrl, upId)
	default:
		panic(fmt.Sprintf("unknown connector type - %s", connectorConfig.Type))
	}
}

func createSettingValidationProcessor(chainSpecific chains_specific.ChainSpecific, options *chains.Options) *validations.ValidationProcessor[validations.ValidationSettingResult] {
	if *options.DisableValidation || *options.DisableSettingsValidation {
		return nil
	}

	validators := chainSpecific.SettingsValidators()
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

func createBlockProcessor(chainSpecific chains_specific.ChainSpecific) blocks.BlockProcessor {
	return chainSpecific.BlockProcessor()
}

func getChainSpecific(
	ctx context.Context,
	conf *config.Upstream,
	upstreamConnectorsInfo *connectorsInfo,
	configuredChain *chains.ConfiguredChain,
) (chains_specific.ChainSpecific, error) {
	//TODO: there might be a few protocols a chain can work with, so it will be necessary to implement all of them
	switch configuredChain.Type {
	case chains.Ethereum:
		if chains.IsTron(configuredChain.Chain) {
			return tron_specific.NewTronSpecific(
				ctx,
				conf.Id,
				upstreamConnectorsInfo.internalRequestConnector,
				configuredChain,
				conf.PollInterval,
				conf.Options,
			)
		}
		return evm_specific.NewEvmChainSpecific(
			ctx,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			upstreamConnectorsInfo.allConnectors,
			configuredChain,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Aztec:
		return aztec_specific.NewAztecChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			conf.Options,
			upstreamConnectorsInfo.internalRequestConnector,
		), nil
	case chains.Algorand:
		return algorand_specific.NewAlgorandChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.Options,
		), nil
	case chains.Bitcoin:
		return bitcoin_specific.NewBitcoinChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.Options,
		), nil
	case chains.EthereumBeaconChain:
		return beacon_specific.NewBeaconChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.PollInterval,
			conf.Options,
		), nil
	case chains.Aptos:
		return aptos_specific.NewAptosChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.Options,
		), nil
	case chains.Near:
		return near_specific.NewNearChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.Options,
		), nil
	case chains.Ripple:
		return ripple_specific.NewRippleChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.Options,
		), nil
	case chains.Solana:
		return solana_specific.NewSolanaChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.Options,
		), nil
	case chains.Starknet:
		return starknet_specific.NewStarknetChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.PollInterval,
			conf.Options,
		), nil
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

func createUpstreamConnectors(
	ctx context.Context,
	conf *config.Upstream,
	configuredChain *chains.ConfiguredChain,
	tracker dimensions.DimensionTracker,
	statsService UpstreamStatsService,
	executor failsafe.Executor[protocol.ResponseHolder],
	torProxyUrl string,
) (*connectorsInfo, error) {
	apiConnectors := make([]connectors.ApiConnector, 0)
	var headConnector connectors.ApiConnector
	var internalRequestConnector connectors.ApiConnector

	for _, connectorConfig := range conf.Connectors {
		apiConnector, err := createConnector(ctx, conf.Id, configuredChain, connectorConfig, torProxyUrl)
		if err != nil {
			return nil, fmt.Errorf("couldn't create api connector of %s: %v", conf.Id, err)
		}
		hooks := []protocol.ResponseReceivedHook{
			dimensions.NewDimensionHook(tracker),
			hook.NewStatsHook(statsService),
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
