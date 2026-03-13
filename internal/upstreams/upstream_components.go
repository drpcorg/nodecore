package upstreams

import (
	"context"
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/stats"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/samber/lo"
)

func createLowerBoundsService(chainSpecific specific.ChainSpecific, options *config.UpstreamOptions) lower_bounds.LowerBoundProcessor {
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
	switch connectorConfig.Type {
	case config.JsonRpc:
		return connectors.NewHttpConnector(connectorConfig, protocol.JsonRpcConnector, torProxyUrl)
	case config.Ws:
		connection, err := ws.NewJsonRpcWsConnection(ctx, configuredChain.Chain, upId, configuredChain.MethodSpec, connectorConfig, torProxyUrl)
		if err != nil {
			return nil, err
		}
		return connectors.NewWsConnector(connection), nil
	case config.Rest:
		return connectors.NewHttpConnector(connectorConfig, protocol.RestConnector, torProxyUrl)
	default:
		panic(fmt.Sprintf("unknown connector type - %s", connectorConfig.Type))
	}
}

func createSettingValidationProcessor(chainSpecific specific.ChainSpecific) *validations.ValidationProcessor[validations.ValidationSettingResult] {
	validators := chainSpecific.SettingsValidators()
	if len(validators) == 0 {
		return nil
	}
	return validations.NewSettingsValidationProcessor(validators)
}

func createHealthValidationProcessor(chainSpecific specific.ChainSpecific, options *config.UpstreamOptions) *validations.ValidationProcessor[protocol.AvailabilityStatus] {
	if *options.DisableValidation && *options.DisableHealthValidation {
		return nil
	}
	validators := chainSpecific.HealthValidators()
	if len(validators) == 0 {
		return nil
	}
	return validations.NewHealthValidationProcessor(validators)
}

func createBlockProcessor(
	ctx context.Context,
	upConfig *config.Upstream,
	connector connectors.ApiConnector,
	chainSpecific specific.ChainSpecific,
	blockchainType chains.BlockchainType,
) blocks.BlockProcessor {
	switch blockchainType {
	case chains.Ethereum:
		return blocks.NewEthLikeBlockProcessor(ctx, upConfig, connector, chainSpecific)
	default:
		return nil
	}
}

func getChainSpecific(
	ctx context.Context,
	conf *config.Upstream,
	upstreamConnectorsInfo *connectorsInfo,
	configuredChain *chains.ConfiguredChain,
) specific.ChainSpecific {
	//TODO: there might be a few protocols a chain can work with, so it will be necessary to implement all of them
	switch configuredChain.Type {
	case chains.Ethereum:
		return specific.NewEvmChainSpecific(conf.Id, upstreamConnectorsInfo.internalRequestConnector, configuredChain, conf.Options)
	case chains.Aztec:
		return specific.NewAztecChainSpecificObject(conf.Id, upstreamConnectorsInfo.internalRequestConnector)
	case chains.Solana:
		return specific.NewSolanaChainSpecificObject(
			ctx,
			configuredChain,
			conf.Id,
			upstreamConnectorsInfo.internalRequestConnector,
			conf.Options.InternalTimeout,
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

func createUpstreamConnectors(
	ctx context.Context,
	conf *config.Upstream,
	configuredChain *chains.ConfiguredChain,
	tracker dimensions.DimensionTracker,
	statsService stats.StatsService,
	executor failsafe.Executor[protocol.ResponseHolder],
	torProxyUrl string,
) (*connectorsInfo, mapset.Set[protocol.Cap], error) {
	caps := mapset.NewThreadUnsafeSet[protocol.Cap]()
	apiConnectors := make([]connectors.ApiConnector, 0)
	var headConnector connectors.ApiConnector
	var internalRequestConnector connectors.ApiConnector

	for _, connectorConfig := range conf.Connectors {
		apiConnector, err := createConnector(ctx, conf.Id, configuredChain, connectorConfig, torProxyUrl)
		if err != nil {
			return nil, nil, fmt.Errorf("couldn't create api connector of %s: %v", conf.Id, err)
		}
		hooks := []protocol.ResponseReceivedHook{
			dimensions.NewDimensionHook(tracker),
			stats.NewStatsHook(statsService),
		}
		apiConnector = connectors.NewObserverConnector(configuredChain.Chain, conf.Id, apiConnector, hooks, executor)
		if connectorConfig.Type == conf.HeadConnector {
			headConnector = apiConnector
		}
		if connectorConfig.Type == conf.GetBestConnector() {
			internalRequestConnector = apiConnector
		}
		if apiConnector.GetType() == protocol.WsConnector {
			caps.Add(protocol.WsCap)
		}
		apiConnectors = append(apiConnectors, apiConnector)
	}

	return newConnectorInfo(headConnector, internalRequestConnector, apiConnectors), caps, nil
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
