package upstreams

import (
	"context"
	"errors"
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
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
	cancelFunc    context.CancelFunc
	mainLifecycle *utils.BaseLifecycle
}

func newUpstreamCtx(cancelFunc context.CancelFunc, mainLifecycle *utils.BaseLifecycle) *upstreamCtx {
	return &upstreamCtx{
		cancelFunc:    cancelFunc,
		mainLifecycle: mainLifecycle,
	}
}

type BaseUpstream struct {
	id               string
	chain            chains.Chain
	vendorType       UpstreamVendor
	apiConnectors    []connectors.ApiConnector
	subManager       *utils.SubscriptionManager[protocol.UpstreamEvent]
	upstreamState    *utils.Atomic[protocol.UpstreamState]
	stateChan        chan protocol.AbstractUpstreamStateEvent
	upstreamIndexHex string
	upConfig         *config.Upstream
	upstreamCtx      *upstreamCtx
	emitter          event_processors.Emitter

	processorAggregator *event_processors.UpstreamProcessorAggregator
}

var _ Upstream = (*BaseUpstream)(nil)

func NewBaseUpstream(
	ctx context.Context,
	cancelFunc context.CancelFunc,
	conf *config.Upstream,
	configuredChain *chains.ConfiguredChain,
	upstreamIndex int,
	creationData *upstreamCreationData,
	processorAggregator *event_processors.UpstreamProcessorAggregator,
) (*BaseUpstream, error) {
	upstreamIndexHex := fmt.Sprintf("%05x", upstreamIndex)

	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(
		protocol.DefaultUpstreamState(
			creationData.upstreamMethods,
			creationData.caps,
			upstreamIndexHex,
			creationData.rt,
			creationData.autoTune,
		),
	)
	stateChan := make(chan protocol.AbstractUpstreamStateEvent, 100)
	emitter := func(event protocol.AbstractUpstreamStateEvent) {
		stateChan <- event
	}
	processorAggregator.SetEmitter(emitter)

	mainLifecycle := utils.NewBaseLifecycle(fmt.Sprintf("%s_main_upstream", conf.Id), ctx)
	return &BaseUpstream{
		id:                  conf.Id,
		chain:               configuredChain.Chain,
		vendorType:          getUpstreamVendor(conf.Connectors),
		apiConnectors:       creationData.upstreamConnectorsInfo.allConnectors,
		upstreamCtx:         newUpstreamCtx(cancelFunc, mainLifecycle),
		upstreamState:       upState,
		subManager:          utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", conf.Id)),
		upstreamIndexHex:    upstreamIndexHex,
		upConfig:            conf,
		processorAggregator: processorAggregator,
		stateChan:           stateChan,
		emitter:             emitter,
	}, nil
}

func NewBaseUpstreamWithParams(
	id string,
	chain chains.Chain,
	apiConnectors []connectors.ApiConnector,
	upConfig *config.Upstream,
	index string,
	upState *utils.Atomic[protocol.UpstreamState],
	processorAggregator *event_processors.UpstreamProcessorAggregator,
	stateChan *chan protocol.AbstractUpstreamStateEvent,
	emitter *event_processors.Emitter,
) *BaseUpstream {
	ctx, cancel := context.WithCancel(context.Background())

	if stateChan == nil {
		stateChan = new(make(chan protocol.AbstractUpstreamStateEvent, 100))
	}
	if emitter == nil {
		var f event_processors.Emitter = func(event protocol.AbstractUpstreamStateEvent) {
			*stateChan <- event
		}
		emitter = &f
	}
	if processorAggregator == nil {
		processorAggregator = &event_processors.UpstreamProcessorAggregator{}
	}
	processorAggregator.SetEmitter(*emitter)

	mainLifecycle := utils.NewBaseLifecycle(fmt.Sprintf("%s_main_upstream", id), ctx)
	return &BaseUpstream{
		id:                  id,
		chain:               chain,
		upstreamCtx:         newUpstreamCtx(cancel, mainLifecycle),
		upstreamState:       upState,
		apiConnectors:       apiConnectors,
		subManager:          utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", "id")),
		upstreamIndexHex:    index,
		upConfig:            upConfig,
		processorAggregator: processorAggregator,
		stateChan:           *stateChan,
		emitter:             *emitter,
	}
}

func (u *BaseUpstream) GetId() string {
	return u.id
}

func (u *BaseUpstream) GetChain() chains.Chain {
	return u.chain
}

func (u *BaseUpstream) Start() {
	u.upstreamCtx.mainLifecycle.Start(func(ctx context.Context) error {
		result, ok := u.processorAggregator.ValidateSettings()
		if !ok {
			u.processorAggregator.StartProcessor(event_processors.SettingsValidatorProcessorType)
			u.Resume()
		} else {
			switch result {
			case validations.FatalSettingError:
				log.Error().Msgf("failed to start upstream '%s' due to invalid upstream settings", u.id)
				return errors.New("invalid upstream settings")
			case validations.SettingsError:
				log.Warn().Msgf("non fatal settings error of upstream '%s', keep validating...", u.id)
				u.processorAggregator.StartProcessor(event_processors.SettingsValidatorProcessorType)
			case validations.Valid:
				u.processorAggregator.StartProcessor(event_processors.SettingsValidatorProcessorType)
				u.Resume()
			case validations.UnknownResult:
				// skip
			}
		}
		go u.processStateEvents(ctx)
		return nil
	})
}

func (u *BaseUpstream) Stop() {
	u.upstreamCtx.mainLifecycle.Stop()
	u.upstreamCtx.cancelFunc()
	u.processorAggregator.StopProcessor(event_processors.SettingsValidatorProcessorType)
	u.PartialStop()
}

func (u *BaseUpstream) Running() bool {
	return u.upstreamCtx.mainLifecycle.Running()
}

func (u *BaseUpstream) PartialStop() {
	u.processorAggregator.StopProcessor(event_processors.BlockEventProcessorType)
	u.processorAggregator.StopProcessor(event_processors.HealthValidatorProcessorType)
	u.processorAggregator.StopProcessor(event_processors.LowerBoundEventProcessorType)
	u.processorAggregator.StopProcessor(event_processors.HeadEventProcessorType)
}

func (u *BaseUpstream) Resume() {
	u.processorAggregator.StartProcessor(event_processors.HeadEventProcessorType)
	u.processorAggregator.StartProcessor(event_processors.BlockEventProcessorType)
	u.processorAggregator.StartProcessor(event_processors.HealthValidatorProcessorType)
	u.processorAggregator.StartProcessor(event_processors.LowerBoundEventProcessorType)

	if u.processorAggregator.IsHealthProcessorDisabled() {
		u.emitter(&protocol.StatusUpstreamStateEvent{Status: protocol.Available})
	}
}

func (u *BaseUpstream) Subscribe(name string) *utils.Subscription[protocol.UpstreamEvent] {
	return u.subManager.Subscribe(name)
}

func (u *BaseUpstream) GetUpstreamState() protocol.UpstreamState {
	return u.upstreamState.Load()
}

func (u *BaseUpstream) GetVendorType() UpstreamVendor {
	return u.vendorType
}

func (u *BaseUpstream) UpdateHead(height, slot uint64) {
	u.processorAggregator.UpdateHead(event_processors.NewHeadUpdateData(height, slot))
}

func (u *BaseUpstream) UpdateBlock(block *protocol.BlockData, blockType protocol.BlockType) {
	u.processorAggregator.UpdateBlock(event_processors.NewBaseBlockUpdateData(block, blockType))
}

func (u *BaseUpstream) BanMethod(method string) {
	u.emitter(&protocol.BanMethodUpstreamStateEvent{Method: method})
}

func (u *BaseUpstream) GetConnector(connectorType protocol.ApiConnectorType) connectors.ApiConnector {
	connector, _ := lo.Find(u.apiConnectors, func(item connectors.ApiConnector) bool {
		return item.GetType() == connectorType
	})
	return connector
}

func (u *BaseUpstream) GetHashIndex() string {
	return u.upstreamIndexHex
}

func (u *BaseUpstream) newUpstreamMethods(bannedMethods mapset.Set[string]) methods.Methods {
	newConfig := &config.MethodsConfig{
		EnableMethods:  u.upConfig.Methods.EnableMethods,
		DisableMethods: lo.Union(bannedMethods.ToSlice(), u.upConfig.Methods.DisableMethods),
	}
	newMethods, _ := methods.NewUpstreamMethods(chains.GetMethodSpecNameByChain(u.chain), newConfig)
	return newMethods
}
