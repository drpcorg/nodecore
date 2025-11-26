package upstreams

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
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

type upstreamCtx struct {
	cancelFunc          context.CancelFunc
	mainLifecycle       *utils.BaseLifecycle
	processorsLifecycle *utils.BaseLifecycle
}

func newUpstreamCtx(cancelFunc context.CancelFunc, mainLifecycle, processorsLifecycle *utils.BaseLifecycle) *upstreamCtx {
	return &upstreamCtx{
		cancelFunc:          cancelFunc,
		mainLifecycle:       mainLifecycle,
		processorsLifecycle: processorsLifecycle,
	}
}

type Upstream struct {
	Id               string
	Chain            chains.Chain
	apiConnectors    []connectors.ApiConnector
	subManager       *utils.SubscriptionManager[protocol.UpstreamEvent]
	upstreamState    *utils.Atomic[protocol.UpstreamState]
	stateChan        chan protocol.AbstractUpstreamStateEvent
	chainSpecific    specific.ChainSpecific
	upstreamIndexHex string
	upConfig         *config.Upstream
	upstreamCtx      *upstreamCtx

	headProcessor               *blocks.HeadProcessor
	blockProcessor              blocks.BlockProcessor
	settingsValidationProcessor *validations.SettingsValidationProcessor
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
		apiConnector, err := createConnector(ctx, conf.Id, configuredChain, connectorConfig, torProxyUrl)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("cound't create api connector of %s: %v", conf.Id, err)
		}
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
	var autoTuneRateLimiter *ratelimiter.UpstreamAutoTune
	if conf.RateLimitAutoTune != nil && conf.RateLimitAutoTune.Enabled {
		autoTuneRateLimiter = ratelimiter.NewUpstreamAutoTune(ctx, conf.Id, conf.RateLimitAutoTune)
	}
	upState.Store(protocol.DefaultUpstreamState(upstreamMethods, caps, upstreamIndexHex, rt, autoTuneRateLimiter))

	mainLifecycle := utils.NewBaseLifecycle(fmt.Sprintf("%s_main_upstream", conf.Id), ctx)
	processorsLifecycle := utils.NewBaseLifecycle(fmt.Sprintf("%s_processors_upstream", conf.Id), ctx)
	return &Upstream{
		Id:               conf.Id,
		Chain:            configuredChain.Chain,
		apiConnectors:    apiConnectors,
		upstreamCtx:      newUpstreamCtx(cancel, mainLifecycle, processorsLifecycle),
		upstreamState:    upState,
		subManager:       utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", conf.Id)),
		stateChan:        make(chan protocol.AbstractUpstreamStateEvent, 100),
		chainSpecific:    chainSpecific,
		upstreamIndexHex: upstreamIndexHex,
		upConfig:         conf,

		headProcessor:               blocks.NewHeadProcessor(ctx, conf, headConnector, chainSpecific),
		blockProcessor:              createBlockProcessor(ctx, conf, headConnector, chainSpecific, configuredChain.Type),
		settingsValidationProcessor: createSettingValidationProcessor(conf.Id, headConnector, configuredChain, chainSpecific, conf.Options),
	}, nil
}

func NewUpstreamWithParams(
	ctx context.Context,
	id string,
	chain chains.Chain,
	apiConnectors []connectors.ApiConnector,
	headProcessor *blocks.HeadProcessor,
	blockProcessor blocks.BlockProcessor,
	settingValidationProcessor *validations.SettingsValidationProcessor,
	state *utils.Atomic[protocol.UpstreamState],
	upstreamIndexHex string,
	upConfig *config.Upstream,
) *Upstream {
	ctx, cancel := context.WithCancel(ctx)

	mainLifecycle := utils.NewBaseLifecycle(fmt.Sprintf("%s_main_upstream", id), ctx)
	processorsLifecycle := utils.NewBaseLifecycle(fmt.Sprintf("%s_processors_upstream", id), ctx)
	return &Upstream{
		Id:                          id,
		Chain:                       chain,
		upstreamCtx:                 newUpstreamCtx(cancel, mainLifecycle, processorsLifecycle),
		upstreamState:               state,
		apiConnectors:               apiConnectors,
		headProcessor:               headProcessor,
		blockProcessor:              blockProcessor,
		subManager:                  utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", "id")),
		stateChan:                   make(chan protocol.AbstractUpstreamStateEvent, 100),
		upstreamIndexHex:            upstreamIndexHex,
		upConfig:                    upConfig,
		settingsValidationProcessor: settingValidationProcessor,
	}
}

func (u *Upstream) Start() {
	u.upstreamCtx.mainLifecycle.Start(func(ctx context.Context) error {
		if u.settingsValidationProcessor != nil && !*u.upConfig.Options.DisableValidation && !*u.upConfig.Options.DisableSettingsValidation {
			result := u.settingsValidationProcessor.ValidateUpstreamSettings()
			switch result {
			case validations.FatalSettingError:
				log.Error().Msgf("failed to start upstream '%s' due to invalid upstream settings", u.Id)
				return errors.New("invalid upstream settings")
			case validations.SettingsError:
				log.Warn().Msgf("non fatal settings error of upstream '%s', keep validating...", u.Id)
				go u.validateUpstreamSettings(ctx, validations.SettingsError)
			case validations.Valid:
				go u.validateUpstreamSettings(ctx, validations.Valid)
				u.Resume()
			}
		} else {
			u.Resume()
		}
		go u.processStateEvents(ctx)
		return nil
	})
}

func (u *Upstream) Stop() {
	u.upstreamCtx.mainLifecycle.Stop()
	u.upstreamCtx.cancelFunc()
	u.PartialStop()
}

func (u *Upstream) Running() bool {
	return u.upstreamCtx.mainLifecycle.Running()
}

func (u *Upstream) ProcessorsRunning() bool {
	return u.upstreamCtx.processorsLifecycle.Running()
}

func (u *Upstream) PartialStop() {
	u.upstreamCtx.processorsLifecycle.Stop()
	if u.blockProcessor != nil {
		u.blockProcessor.Stop()
	}
	u.headProcessor.Stop()
}

func (u *Upstream) Resume() {
	u.upstreamCtx.processorsLifecycle.Start(func(ctx context.Context) error {
		u.start(ctx)
		return nil
	})
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

func (u *Upstream) start(ctx context.Context) {
	go u.headProcessor.Start()
	if u.blockProcessor != nil {
		go u.blockProcessor.Start()
	}

	go u.processBlocks(ctx)
}

func (u *Upstream) validateUpstreamSettings(ctx context.Context, currentValidationState validations.ValidationSettingResult) {
	if !*u.upConfig.Options.DisableValidation && !*u.upConfig.Options.DisableSettingsValidation {
		for {
			select {
			case <-ctx.Done():
				log.Info().Msgf("stopping setting validations of upstream '%s'", u.Id)
				return
			case <-time.After(u.upConfig.Options.ValidationInterval):
				validationResult := u.settingsValidationProcessor.ValidateUpstreamSettings()
				switch validationResult {
				case validations.FatalSettingError:
					if currentValidationState == validations.SettingsError || currentValidationState == validations.Valid {
						log.Warn().Msgf("upstream '%s' settings are invalid, it will be stopped", u.Id)
						u.publishUpstreamStateEvent(&protocol.FatalErrorUpstreamStateEvent{})
						currentValidationState = validations.FatalSettingError
					}
				case validations.SettingsError:
					log.Debug().Msg("keep validating...")
				case validations.Valid:
					if currentValidationState == validations.SettingsError || currentValidationState == validations.FatalSettingError {
						log.Warn().Msgf("upstream '%s' settings are valid", u.Id)
						u.publishUpstreamStateEvent(&protocol.ValidUpstreamStateEvent{})
						currentValidationState = validations.Valid
					}
				}
			}
		}
	}
}

func (u *Upstream) newUpstreamMethods(bannedMethods mapset.Set[string]) methods.Methods {
	newConfig := &config.MethodsConfig{
		EnableMethods:  u.upConfig.Methods.EnableMethods,
		DisableMethods: lo.Union(bannedMethods.ToSlice(), u.upConfig.Methods.DisableMethods),
	}
	newMethods, _ := methods.NewUpstreamMethods(chains.GetMethodSpecNameByChain(u.Chain), newConfig)
	return newMethods
}

func (u *Upstream) publishUpstreamStateEvent(event protocol.AbstractUpstreamStateEvent) {
	u.stateChan <- event
}

func (u *Upstream) processBlocks(ctx context.Context) {
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
		case <-ctx.Done():
			log.Info().Msgf("stopping block processing of upstream '%s'", u.Id)
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

func createConnector(
	ctx context.Context,
	upId string,
	configuredChain chains.ConfiguredChain,
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

func createSettingValidationProcessor(
	upstreamId string,
	connector connectors.ApiConnector,
	configuredChain chains.ConfiguredChain,
	chainSpecific specific.ChainSpecific,
	options *config.UpstreamOptions,
) *validations.SettingsValidationProcessor {
	validators := chainSpecific.SettingsValidators(upstreamId, connector, configuredChain, options)
	if len(validators) == 0 {
		return nil
	}
	return validations.NewSettingsValidationProcessor(validators)
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
