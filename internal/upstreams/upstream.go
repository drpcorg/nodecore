package upstreams

import (
	"context"
	"fmt"
	"slices"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

var blocksMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "blocks",
		Help:      "The current block height of a specific block type",
	},
	[]string{"upstream", "blockType", "chain"},
)

var headsMetric = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "upstream",
		Name:      "heads",
		Help:      "The current head height",
	},
	[]string{"chain", "upstream"},
)

func init() {
	prometheus.MustRegister(blocksMetric, headsMetric)
}

type Upstream struct {
	Id               string
	Chain            chains.Chain
	apiConnectors    []connectors.ApiConnector
	ctx              context.Context
	headProcessor    *blocks.HeadProcessor
	blockProcessor   blocks.BlockProcessor
	subManager       *utils.SubscriptionManager[protocol.UpstreamEvent]
	cancelFunc       context.CancelFunc
	upstreamState    *utils.Atomic[protocol.UpstreamState]
	stateChan        chan protocol.AbstractUpstreamStateEvent
	chainSpecific    specific.ChainSpecific
	upstreamIndexHex string
	methodsConfig    *config.MethodsConfig
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
	conf *config.Upstream,
	tracker *dimensions.DimensionTracker,
	executor failsafe.Executor[protocol.ResponseHolder],
	upstreamIndex int,
	rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
	torProxyUrl string,
) (*Upstream, error) {
	ctx, cancel := context.WithCancel(ctx)
	configuredChain := chains.GetChain(conf.ChainName)
	apiConnectors := make([]connectors.ApiConnector, 0)
	caps := mapset.NewThreadUnsafeSet[protocol.Cap]()

	var headConnector connectors.ApiConnector
	for _, connectorConfig := range conf.Connectors {
		apiConnector := createConnector(ctx, conf.Id, configuredChain, connectorConfig, torProxyUrl)
		apiConnector = connectors.NewDimensionTrackerConnector(configuredChain.Chain, conf.Id, apiConnector, tracker, executor)
		if connectorConfig.Type == conf.HeadConnector {
			headConnector = apiConnector
		}
		if apiConnector.GetType() == protocol.WsConnector {
			caps.Add(protocol.WsCap)
		}
		apiConnectors = append(apiConnectors, apiConnector)
	}
	chainSpecific := getChainSpecific(configuredChain.Type)

	upstreamMethods, err := methods.NewUpstreamMethods(configuredChain.MethodSpec, conf.Methods)
	if err != nil {
		cancel()
		return nil, err
	}
	upstreamIndexHex := fmt.Sprintf("%05x", upstreamIndex)

	upState := utils.NewAtomic[protocol.UpstreamState]()
	var rt *ratelimiter.RateLimitBudget
	if conf.RateLimit != nil {
		rt = ratelimiter.NewRateLimitBudget(&config.RateLimitBudget{
			Name:   "inplace",
			Config: conf.RateLimit,
		}, ratelimiter.NewRateLimitMemoryEngine())
	} else if conf.RateLimitBudget != "" {
		rateLimitBudget, ok := rateLimitBudgetRegistry.Get(conf.RateLimitBudget)
		if !ok {
			zerolog.Ctx(ctx).Panic().Msgf("rate limit budget %s not found", conf.RateLimitBudget)
		}
		rt = rateLimitBudget
	}
	upState.Store(protocol.DefaultUpstreamState(upstreamMethods, caps, upstreamIndexHex, rt))

	return &Upstream{
		Id:               conf.Id,
		Chain:            configuredChain.Chain,
		apiConnectors:    apiConnectors,
		ctx:              ctx,
		cancelFunc:       cancel,
		upstreamState:    upState,
		headProcessor:    blocks.NewHeadProcessor(ctx, conf, headConnector, chainSpecific),
		blockProcessor:   createBlockProcessor(ctx, conf, headConnector, chainSpecific, configuredChain.Type),
		subManager:       utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", conf.Id)),
		stateChan:        make(chan protocol.AbstractUpstreamStateEvent, 100),
		chainSpecific:    chainSpecific,
		upstreamIndexHex: upstreamIndexHex,
		methodsConfig:    conf.Methods,
	}, nil
}

func NewUpstreamWithParams(
	ctx context.Context,
	id string,
	chain chains.Chain,
	apiConnectors []connectors.ApiConnector,
	headProcessor *blocks.HeadProcessor,
	blockProcessor blocks.BlockProcessor,
	state *utils.Atomic[protocol.UpstreamState],
	upstreamIndexHex string,
	methodsConfig *config.MethodsConfig,
) *Upstream {
	ctx, cancel := context.WithCancel(ctx)

	return &Upstream{
		Id:               id,
		Chain:            chain,
		ctx:              ctx,
		cancelFunc:       cancel,
		upstreamState:    state,
		apiConnectors:    apiConnectors,
		headProcessor:    headProcessor,
		blockProcessor:   blockProcessor,
		subManager:       utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", "id")),
		stateChan:        make(chan protocol.AbstractUpstreamStateEvent, 100),
		upstreamIndexHex: upstreamIndexHex,
		methodsConfig:    methodsConfig,
	}
}

func (u *Upstream) Subscribe(name string) *utils.Subscription[protocol.UpstreamEvent] {
	return u.subManager.Subscribe(name)
}

func (u *Upstream) GetUpstreamState() protocol.UpstreamState {
	return u.upstreamState.Load()
}

func (u *Upstream) UpdateHead(height, slot uint64) {
	u.headProcessor.UpdateHead(height, slot)
}

func (u *Upstream) UpdateBlock(block *protocol.BlockData, blockType protocol.BlockType) {
	u.blockProcessor.UpdateBlock(block, blockType)
}

func (u *Upstream) BanMethod(method string) {
	u.publishUpstreamStateEvent(&protocol.BanMethodUpstreamStateEvent{Method: method})
}

func (u *Upstream) GetConnector(connectorType protocol.ApiConnectorType) connectors.ApiConnector {
	connector, _ := lo.Find(u.apiConnectors, func(item connectors.ApiConnector) bool {
		return item.GetType() == connectorType
	})
	return connector
}

func (u *Upstream) GetHashIndex() string {
	return u.upstreamIndexHex
}

// update upstream state through one pipeline
func (u *Upstream) processStateEvents() {
	bannedMethods := mapset.NewThreadUnsafeSet[string]()
	for {
		select {
		case <-u.ctx.Done():
			return
		case event := <-u.stateChan:
			state := u.upstreamState.Load()

			switch stateEvent := event.(type) {
			case *protocol.HeadUpstreamStateEvent:
				if state.HeadData != nil && state.HeadData.IsEmpty() {
					state.Status = protocol.Available
				}
				state.HeadData = stateEvent.HeadData
			case *protocol.BlockUpstreamStateEvent:
				state.BlockInfo.AddBlock(stateEvent.BlockData, stateEvent.BlockType)
			case *protocol.BanMethodUpstreamStateEvent:
				if bannedMethods.ContainsOne(stateEvent.Method) || slices.Contains(u.methodsConfig.EnableMethods, stateEvent.Method) {
					continue
				}
				time.AfterFunc(u.methodsConfig.BanDuration, func() {
					u.publishUpstreamStateEvent(&protocol.UnbanMethodUpstreamStateEvent{Method: stateEvent.Method})
				})
				log.Warn().Msgf("the method %s has been banned on upstream %s", stateEvent.Method, u.Id)
				bannedMethods.Add(stateEvent.Method)
				state.UpstreamMethods = u.newUpstreamMethods(bannedMethods)
			case *protocol.UnbanMethodUpstreamStateEvent:
				if !bannedMethods.ContainsOne(stateEvent.Method) {
					continue
				}
				log.Warn().Msgf("the method %s has been unbanned on upstream %s", stateEvent.Method, u.Id)
				bannedMethods.Remove(stateEvent.Method)
				state.UpstreamMethods = u.newUpstreamMethods(bannedMethods)
			default:
				panic(fmt.Sprintf("unknown event type %T", event))
			}

			u.upstreamState.Store(state)
			upstreamEvent := u.createEvent()

			u.subManager.Publish(upstreamEvent)
		}
	}
}

func (u *Upstream) newUpstreamMethods(bannedMethods mapset.Set[string]) methods.Methods {
	newConfig := &config.MethodsConfig{
		EnableMethods:  u.methodsConfig.EnableMethods,
		DisableMethods: lo.Union(bannedMethods.ToSlice(), u.methodsConfig.DisableMethods),
	}
	newMethods, _ := methods.NewUpstreamMethods(chains.GetMethodSpecNameByChain(u.Chain), newConfig)
	return newMethods
}

func (u *Upstream) createEvent() protocol.UpstreamEvent {
	state := u.upstreamState.Load()
	return protocol.UpstreamEvent{
		Id:    u.Id,
		Chain: u.Chain,
		State: &state,
	}
}

func (u *Upstream) publishUpstreamStateEvent(event protocol.AbstractUpstreamStateEvent) {
	u.stateChan <- event
}

func (u *Upstream) processHeads() {
	var blockChan chan blocks.BlockEvent
	if u.blockProcessor != nil {
		blockSub := u.blockProcessor.Subscribe(fmt.Sprintf("%s_block_updates", u.Id))
		defer blockSub.Unsubscribe()
		blockChan = blockSub.Events
	} else {
		blockChan = make(chan blocks.BlockEvent)
	}

	headSub := u.headProcessor.Subscribe(fmt.Sprintf("%s_head_updates", u.Id))
	defer headSub.Unsubscribe()

	for {
		select {
		case <-u.ctx.Done():
			return
		case head, ok := <-headSub.Events:
			if ok {
				u.publishUpstreamStateEvent(&protocol.HeadUpstreamStateEvent{HeadData: head.HeadData})
				headsMetric.WithLabelValues(u.Chain.String(), u.Id).Set(float64(head.HeadData.Height))
			}
		case block, ok := <-blockChan:
			if ok {
				u.publishUpstreamStateEvent(&protocol.BlockUpstreamStateEvent{BlockData: block.BlockData, BlockType: block.BlockType})
				blocksMetric.WithLabelValues(u.Id, block.BlockType.String(), u.Chain.String()).Set(float64(block.BlockData.Height))
			}
		}
	}
}

func (u *Upstream) handleSubscriptions() {
	go u.processStateEvents()
	go u.processHeads()
}

func createConnector(ctx context.Context, upId string, configuredChain chains.ConfiguredChain, connectorConfig *config.ApiConnectorConfig, torProxyUrl string) connectors.ApiConnector {
	switch connectorConfig.Type {
	case config.JsonRpc:
		return connectors.NewHttpConnector(connectorConfig.Url, protocol.JsonRpcConnector, connectorConfig.Headers, torProxyUrl)
	case config.Ws:
		connection := ws.NewJsonRpcWsConnection(ctx, configuredChain.Chain, upId, configuredChain.MethodSpec, connectorConfig.Url, connectorConfig.Headers, torProxyUrl)
		return connectors.NewWsConnector(connection)
	case config.Rest:
		return connectors.NewHttpConnector(connectorConfig.Url, protocol.RestConnector, connectorConfig.Headers, torProxyUrl)
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
