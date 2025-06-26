package upstreams

import (
	"context"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/dimensions"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams/blocks"
	"github.com/drpcorg/dsheltie/internal/upstreams/chains_specific"
	"github.com/drpcorg/dsheltie/internal/upstreams/connectors"
	"github.com/drpcorg/dsheltie/internal/upstreams/methods"
	"github.com/drpcorg/dsheltie/internal/upstreams/ws"
	"github.com/drpcorg/dsheltie/pkg/chains"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/samber/lo"
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
	Id             string
	Chain          chains.Chain
	apiConnectors  []connectors.ApiConnector
	ctx            context.Context
	headProcessor  *blocks.HeadProcessor
	blockProcessor blocks.BlockProcessor
	subManager     *utils.SubscriptionManager[protocol.UpstreamEvent]
	cancelFunc     context.CancelFunc
	upstreamState  *utils.Atomic[protocol.UpstreamState]
	stateChan      chan protocol.AbstractUpstreamStateEvent
	chainSpecific  specific.ChainSpecific
}

func (u *Upstream) Start() {
	go u.headProcessor.Start()
	if u.blockProcessor != nil {
		go u.blockProcessor.Start()
	}

	u.handleSubscriptions()
}

func (u *Upstream) Stop() {
	u.cancelFunc()
}

func NewUpstream(
	ctx context.Context,
	config *config.Upstream,
	tracker *dimensions.DimensionTracker,
	executor failsafe.Executor[protocol.ResponseHolder],
) (*Upstream, error) {
	ctx, cancel := context.WithCancel(ctx)
	configuredChain := chains.GetChain(config.ChainName)
	apiConnectors := make([]connectors.ApiConnector, 0)
	caps := mapset.NewThreadUnsafeSet[protocol.Cap]()

	var headConnector connectors.ApiConnector
	for _, connectorConfig := range config.Connectors {
		apiConnector := createConnector(ctx, config.Id, configuredChain.MethodSpec, connectorConfig)
		apiConnector = connectors.NewDimensionTrackerConnector(configuredChain.Chain, config.Id, apiConnector, tracker, executor)
		if connectorConfig.Type == config.HeadConnector {
			headConnector = apiConnector
		}
		if apiConnector.GetType() == protocol.WsConnector {
			caps.Add(protocol.WsCap)
		}
		apiConnectors = append(apiConnectors, apiConnector)
	}
	chainSpecific := getChainSpecific(configuredChain.Type)

	upstreamMethods, err := methods.NewUpstreamMethods(configuredChain.MethodSpec, config.Methods)
	if err != nil {
		cancel()
		return nil, err
	}

	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(protocol.DefaultUpstreamState(upstreamMethods, caps))

	return &Upstream{
		Id:             config.Id,
		Chain:          configuredChain.Chain,
		apiConnectors:  apiConnectors,
		ctx:            ctx,
		cancelFunc:     cancel,
		upstreamState:  upState,
		headProcessor:  blocks.NewHeadProcessor(ctx, config, headConnector, chainSpecific),
		blockProcessor: createBlockProcessor(ctx, config, headConnector, chainSpecific, configuredChain.Type),
		subManager:     utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", config.Id)),
		stateChan:      make(chan protocol.AbstractUpstreamStateEvent, 100),
		chainSpecific:  chainSpecific,
	}, nil
}

func NewUpstreamWithParams(
	ctx context.Context,
	id string,
	chain chains.Chain,
	apiConnectors []connectors.ApiConnector,
	headProcessor *blocks.HeadProcessor,
	state *utils.Atomic[protocol.UpstreamState],
) *Upstream {
	ctx, cancel := context.WithCancel(ctx)

	return &Upstream{
		Id:            id,
		Chain:         chain,
		ctx:           ctx,
		cancelFunc:    cancel,
		upstreamState: state,
		apiConnectors: apiConnectors,
		headProcessor: headProcessor,
		subManager:    utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", "id")),
		stateChan:     make(chan protocol.AbstractUpstreamStateEvent, 100),
	}
}

func (u *Upstream) Subscribe(name string) *utils.Subscription[protocol.UpstreamEvent] {
	return u.subManager.Subscribe(name)
}

func (u *Upstream) GetUpstreamState() protocol.UpstreamState {
	return u.upstreamState.Load()
}

func (u *Upstream) GetConnector(connectorType protocol.ApiConnectorType) connectors.ApiConnector {
	connector, _ := lo.Find(u.apiConnectors, func(item connectors.ApiConnector) bool {
		return item.GetType() == connectorType
	})
	return connector
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
			case *protocol.BlockUpstreamStateEvent:
				state.BlockInfo.AddBlock(stateEvent.BlockData, stateEvent.BlockType)
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

func (u *Upstream) processBlocks() {
	sub := u.blockProcessor.Subscribe(fmt.Sprintf("%s_block_updates", u.Id))
	defer sub.Unsubscribe()

	for {
		select {
		case <-u.ctx.Done():
			return
		case e, ok := <-sub.Events:
			if ok {
				u.publishUpstreamStateEvent(&protocol.BlockUpstreamStateEvent{BlockData: e.BlockData, BlockType: e.BlockType})
			}
		}
	}
}

func (u *Upstream) handleSubscriptions() {
	go u.processStateEvents()
	go u.processHeads()
	if u.blockProcessor != nil {
		go u.processBlocks()
	}
}

func createConnector(ctx context.Context, upId, methodSpec string, connectorConfig *config.ApiConnectorConfig) connectors.ApiConnector {
	switch connectorConfig.Type {
	case config.JsonRpc:
		return connectors.NewHttpConnector(connectorConfig.Url, protocol.JsonRpcConnector, connectorConfig.Headers)
	case config.Ws:
		connection := ws.NewJsonRpcWsConnection(ctx, upId, methodSpec, connectorConfig.Url, connectorConfig.Headers)
		return connectors.NewWsConnector(connection)
	case config.Rest:
		return connectors.NewHttpConnector(connectorConfig.Url, protocol.RestConnector, connectorConfig.Headers)
	default:
		panic(fmt.Sprintf("unknown connector type - %s", connectorConfig.Type))
	}
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
