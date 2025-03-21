package upstreams

import (
	"context"
	"fmt"
	"github.com/drpcorg/dshaltie/internal/config"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dshaltie/internal/upstreams/connectors"
	"github.com/drpcorg/dshaltie/internal/upstreams/heads"
	"github.com/drpcorg/dshaltie/internal/upstreams/ws"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/drpcorg/dshaltie/pkg/utils"
)

func (u *Upstream) createEvent() protocol.UpstreamEvent {
	state := u.upstreamState.Load()
	return protocol.UpstreamEvent{
		Id:    u.Id,
		Chain: u.Chain,
		State: &state,
	}
}

type Upstream struct {
	Id            string
	Chain         chains.Chain
	apiConnectors []connectors.ApiConnector
	ctx           context.Context
	headProcessor *heads.HeadProcessor
	subManager    *utils.SubscriptionManager[protocol.UpstreamEvent]
	cancelFunc    context.CancelFunc
	upstreamState *utils.Atomic[protocol.UpstreamState]
	stateChan     chan protocol.AbstractUpstreamStateEvent
}

func (u *Upstream) Start() {
	go u.processStateEvents()

	go u.headProcessor.Start()

	go u.processHeads()
}

func (u *Upstream) Stop() {
	u.cancelFunc()
}

func NewUpstream(ctx context.Context, config *config.Upstream) *Upstream {
	ctx, cancel := context.WithCancel(ctx)
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

	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(protocol.UpstreamState{Status: protocol.Available})

	return &Upstream{
		Id:            config.Id,
		Chain:         configuredChain.Chain,
		apiConnectors: apiConnectors,
		ctx:           ctx,
		cancelFunc:    cancel,
		upstreamState: upState,
		headProcessor: heads.NewHeadProcessor(ctx, config, headConnector, chainSpecific),
		subManager:    utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", config.Id)),
		stateChan:     make(chan protocol.AbstractUpstreamStateEvent, 100),
	}
}

func (u *Upstream) Subscribe(name string) *utils.Subscription[protocol.UpstreamEvent] {
	return u.subManager.Subscribe(name)
}

func (u *Upstream) GetUpstreamState() protocol.UpstreamState {
	return u.upstreamState.Load()
}

// update upstream state through one pipeline
func (u *Upstream) processStateEvents() {
	for {
		select {
		case <-u.ctx.Done():
			return
		case event := <-u.stateChan:
			state := u.upstreamState.Load()

			switch stateEvent := event.(type) {
			case *protocol.HeadUpstreamStateEvent:
				state.HeadData = stateEvent.HeadData
			default:
				panic(fmt.Sprintf("unknown event type %T", event))
			}

			u.upstreamState.Store(state)
			upstreamEvent := u.createEvent()

			u.subManager.Publish(upstreamEvent)
		}
	}
}

func (u *Upstream) publishUpstreamStateEvent(event protocol.AbstractUpstreamStateEvent) {
	u.stateChan <- event
}

func (u *Upstream) processHeads() {
	sub := u.headProcessor.Subscribe(fmt.Sprintf("%s_head_updates", u.Id))
	defer sub.Unsubscribe()

	for {
		select {
		case <-u.ctx.Done():
			return
		case e, ok := <-sub.Events:
			if ok {
				u.publishUpstreamStateEvent(&protocol.HeadUpstreamStateEvent{HeadData: e.HeadData})
			}
		}
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
		panic(fmt.Sprintf("unknown connector type - %s", connectorConfig.Type))
	}
}

func getChainSpecific(blockchainType chains.BlockchainType) specific.ChainSpecific {
	//TODO: there might be a few protocols a chain can work with, so it will be necessary to implement all of them
	switch blockchainType {
	case chains.Ethereum:
		return specific.EvmChainSpecific
	case chains.Solana:
		return specific.SolanaChainSpecific
	default:
		panic(fmt.Sprintf("unknown blockchain type - %s", blockchainType))
	}
}
