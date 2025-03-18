package upstreams

import (
	"context"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dshaltie/internal/upstreams/connectors"
	"github.com/drpcorg/dshaltie/internal/upstreams/ws"
	"github.com/drpcorg/dshaltie/pkg/chains"
)

type UpstreamStatus int

const (
	UpstreamAvailable UpstreamStatus = iota
	UpstreamLagging
	UpstreamUnavailable
)

type Upstream struct {
	Id            string
	Chain         chains.Chain
	Status        UpstreamStatus
	apiConnectors []connectors.ApiConnector
	chainSpecific specific.ChainSpecific
	ctx           context.Context
	headProcessor *HeadProcessor
}

func (u *Upstream) Start() {
	go u.headProcessor.Start()
}

func NewUpstream(config *config.Upstream) *Upstream {
	ctx := context.Background()
	configuredChain := chains.GetChain(config.ChainName)
	apiConnectors := make([]connectors.ApiConnector, 0)

	var headConnector connectors.ApiConnector
	for _, connectorConfig := range config.Connectors {
		apiConnector := createConnector(ctx, connectorConfig)
		if connectorConfig.Type == config.HeadConnector {
			headConnector = apiConnector
		}
		apiConnectors = append(apiConnectors, apiConnector)
	}
	chainSpecific := getChainSpecific(configuredChain.Type)

	return &Upstream{
		Id:            config.Id,
		Chain:         configuredChain.Chain,
		Status:        UpstreamAvailable,
		apiConnectors: apiConnectors,
		chainSpecific: chainSpecific,
		ctx:           ctx,
		headProcessor: NewHeadProcessor(ctx, config, configuredChain, headConnector, chainSpecific),
	}
}

func createConnector(ctx context.Context, connectorConfig *config.ConnectorConfig) connectors.ApiConnector {
	switch connectorConfig.Type {
	case config.JsonRpc:
		return connectors.NewHttpConnector(connectorConfig.Url, protocol.JsonRpcConnector, connectorConfig.Headers)
	case config.Ws:
		connection := ws.NewWsConnection(ctx, connectorConfig.Url, connectorConfig.Headers)
		return connectors.NewWsConnector(connection)
	case config.Rest:
		return connectors.NewHttpConnector(connectorConfig.Url, protocol.RestConnector, connectorConfig.Headers)
	default:
		return nil
	}
}

func getChainSpecific(blockchainType chains.BlockchainType) specific.ChainSpecific {
	switch blockchainType {
	case chains.Ethereum:
		return specific.EvmChainSpecific
	case chains.Solana:
		return specific.SolanaChainSpecific
	default:
		return nil
	}
}
