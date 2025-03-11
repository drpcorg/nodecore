package upstreams

import (
	"context"
	"github.com/drpcorg/dshaltie/src/chains"
	"github.com/drpcorg/dshaltie/src/config"
	"github.com/drpcorg/dshaltie/src/protocol"
	specific "github.com/drpcorg/dshaltie/src/upstreams/chains_specific"
	"github.com/drpcorg/dshaltie/src/upstreams/connectors"
	"github.com/drpcorg/dshaltie/src/upstreams/ws"
	"github.com/samber/lo"
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

func NewUpstream(config *config.UpstreamConfig) *Upstream {
	ctx := context.Background()
	configuredChain := chains.GetChain(config.ChainName)
	apiConnectors := make([]connectors.ApiConnector, 0)

	for _, connectorConfig := range config.Connectors {
		apiConnectors = append(apiConnectors, createConnector(ctx, connectorConfig))
	}
	chainSpecific := getChainSpecific(configuredChain.Type)
	headConnector := lo.MinBy(apiConnectors, func(a connectors.ApiConnector, b connectors.ApiConnector) bool {
		return a.GetType() < b.GetType()
	})

	return &Upstream{
		Id:            config.Id,
		Chain:         configuredChain.Chain,
		Status:        UpstreamAvailable,
		apiConnectors: apiConnectors,
		chainSpecific: chainSpecific,
		ctx:           ctx,
		headProcessor: NewHeadProcessor(ctx, config.Id, configuredChain, headConnector, chainSpecific),
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
	default:
		return nil
	}
}
