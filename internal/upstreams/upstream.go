package upstreams

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/method-specs/pkg/methods"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type upstreamCtx struct {
	cancelFunc    context.CancelFunc
	mainLifecycle *utils.GenericLifecycle
}

func newUpstreamCtx(cancelFunc context.CancelFunc, mainLifecycle *utils.GenericLifecycle) *upstreamCtx {
	return &upstreamCtx{
		cancelFunc:    cancelFunc,
		mainLifecycle: mainLifecycle,
	}
}

type GenericUpstream struct {
	id               string
	configuredChain  *chains.ConfiguredChain
	vendorType       UpstreamVendor
	apiConnectors    []connectors.ApiConnector
	subManager       *utils.SubscriptionManager[protocol.UpstreamEvent]
	upstreamState    *utils.Atomic[protocol.UpstreamState]
	stateChan        chan protocol.AbstractUpstreamStateEvent
	upstreamIndexHex string
	upConfig         *config.Upstream
	groupLabels      mapset.Set[string]
	upstreamCtx      *upstreamCtx
	emitter          event_processors.Emitter

	headLag atomic.Int64

	processorAggregator *event_processors.UpstreamProcessorAggregator
}

// groupLabelsFromConfig builds the immutable set of config-defined group-labels
// once at upstream construction so label-balancing lookups are O(1).
func groupLabelsFromConfig(conf *config.Upstream) mapset.Set[string] {
	if conf == nil {
		return mapset.NewThreadUnsafeSet[string]()
	}
	return mapset.NewThreadUnsafeSet(conf.GroupLabels...)
}

var _ Upstream = (*GenericUpstream)(nil)

func NewGenericUpstream(
	ctx context.Context,
	cancelFunc context.CancelFunc,
	conf *config.Upstream,
	configuredChain *chains.ConfiguredChain,
	upstreamIndex int,
	creationData *upstreamCreationData,
) (*GenericUpstream, error) {
	upstreamIndexHex := fmt.Sprintf("%05x", upstreamIndex)

	upState := utils.NewAtomic[protocol.UpstreamState]()
	initialState := protocol.DefaultUpstreamState(
		creationData.upstreamMethods,
		mapset.NewThreadUnsafeSet[protocol.Cap](),
		upstreamIndexHex,
		creationData.rt,
		creationData.autoTune,
	)
	// Config-defined labels are seeds: they are published before any detector runs and
	// survive when label detection is disabled, but a detector that owns the same key
	// overwrites them on its first round.
	for label, value := range conf.Labels {
		initialState.Labels.AddLabel(label, value)
	}
	upState.Store(initialState)
	stateChan := make(chan protocol.AbstractUpstreamStateEvent, 1000)
	emitter := func(event protocol.AbstractUpstreamStateEvent) {
		stateChan <- event
	}

	mainLifecycle := utils.NewGenericLifecycle(fmt.Sprintf("%s_main_upstream", conf.Id), ctx)
	upstream := &GenericUpstream{
		id:               conf.Id,
		configuredChain:  configuredChain,
		vendorType:       getUpstreamVendor(conf.Connectors),
		apiConnectors:    creationData.upstreamConnectorsInfo.allConnectors,
		upstreamCtx:      newUpstreamCtx(cancelFunc, mainLifecycle),
		upstreamState:    upState,
		subManager:       utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", conf.Id)),
		upstreamIndexHex: upstreamIndexHex,
		upConfig:         conf,
		groupLabels:      groupLabelsFromConfig(conf),
		stateChan:        stateChan,
		emitter:          emitter,
	}

	chainSpecific, err := getChainSpecific(ctx, conf, creationData.upstreamConnectorsInfo, configuredChain)
	if err != nil {
		return nil, err
	}
	headProcessor := CreateHeadProcessor(ctx, conf, creationData.upstreamConnectorsInfo.headConnector, chainSpecific)
	processorAggregator := event_processors.NewUpstreamProcessorAggregator(
		[]event_processors.UpstreamStateEventProcessor{
			CreateBlockEventProcessor(ctx, conf, chainSpecific, configuredChain),
			CreateHeadEventProcessor(ctx, conf, configuredChain.Chain, headProcessor),
			CreateLowerBoundsEventProcessor(ctx, conf, chainSpecific),
			CreateHealthEventProcessor(ctx, conf, chainSpecific),
			CreateSettingsEventProcessor(ctx, conf, chainSpecific),
			CreateLabelsEventProcessor(ctx, conf, chainSpecific),
			CreateMethodsEventProcessor(ctx, conf, chainSpecific),
			CreateCapEventProcessor(ctx, conf, chainSpecific, creationData.upstreamConnectorsInfo, creationData.upstreamMethods, headProcessor),
		},
	)
	processorAggregator.SetEmitter(emitter)
	upstream.processorAggregator = processorAggregator

	return upstream, nil
}

func NewGenericUpstreamWithParams(
	id string,
	chain chains.Chain,
	apiConnectors []connectors.ApiConnector,
	upConfig *config.Upstream,
	index string,
	upState *utils.Atomic[protocol.UpstreamState],
	processorAggregator *event_processors.UpstreamProcessorAggregator,
	stateChan *chan protocol.AbstractUpstreamStateEvent,
	emitter *event_processors.Emitter,
) *GenericUpstream {
	ctx, cancel := context.WithCancel(context.Background())

	if stateChan == nil {
		stateChan = new(make(chan protocol.AbstractUpstreamStateEvent, 1000))
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

	mainLifecycle := utils.NewGenericLifecycle(fmt.Sprintf("%s_main_upstream", id), ctx)
	return &GenericUpstream{
		id:                  id,
		configuredChain:     chains.GetChain(chain.String()),
		upstreamCtx:         newUpstreamCtx(cancel, mainLifecycle),
		upstreamState:       upState,
		apiConnectors:       apiConnectors,
		subManager:          utils.NewSubscriptionManager[protocol.UpstreamEvent](fmt.Sprintf("%s_upstream", id)),
		upstreamIndexHex:    index,
		upConfig:            upConfig,
		groupLabels:         groupLabelsFromConfig(upConfig),
		processorAggregator: processorAggregator,
		stateChan:           *stateChan,
		emitter:             *emitter,
	}
}

func (u *GenericUpstream) PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64 {
	predicted := int64(0)
	if u.processorAggregator != nil {
		predicted = u.processorAggregator.PredictLowerBound(boundType, timeOffset)
	}
	state := u.upstreamState.Load()
	if state.LowerBoundsInfo != nil {
		if bound, ok := state.LowerBoundsInfo.GetLowerBound(boundType); ok && bound.Bound > predicted {
			predicted = bound.Bound
		}
	}
	return predicted
}

func (u *GenericUpstream) GetCurrentHeadHeight() uint64 {
	state := u.GetUpstreamState()
	return state.HeadData.Height
}

func (u *GenericUpstream) GetId() string {
	return u.id
}

func (u *GenericUpstream) GetChain() chains.Chain {
	return u.configuredChain.Chain
}

func (u *GenericUpstream) GetGroupLabels() mapset.Set[string] {
	if u.groupLabels == nil {
		return mapset.NewThreadUnsafeSet[string]()
	}
	return u.groupLabels
}

func (u *GenericUpstream) Start() {
	u.upstreamCtx.mainLifecycle.Start(func(ctx context.Context) error {
		u.startConnectors(ctx)

		result, ok := u.processorAggregator.ValidateSettings()
		initialValid := true
		if !ok {
			u.processorAggregator.StartProcessor(event_processors.SettingsValidatorProcessorType)
			u.Resume()
		} else {
			switch result {
			case validations.FatalSettingError:
				log.Error().Msgf("failed to start upstream '%s' due to invalid upstream settings", u.id)
				return errors.New("invalid upstream settings")
			case validations.SettingsError:
				initialValid = false
				log.Warn().Msgf("non fatal settings error of upstream '%s', keep validating...", u.id)
				u.processorAggregator.StartProcessor(event_processors.SettingsValidatorProcessorType)
			case validations.Valid:
				u.processorAggregator.StartProcessor(event_processors.SettingsValidatorProcessorType)
				u.Resume()
			case validations.UnknownResult:
				log.Debug().Msgf("upstream '%s' has unknown result of settings validation, skipping", u.id)
			}
		}
		u.emitter(&protocol.InitUpstreamStateEvent{})
		go u.processStateEvents(ctx, initialValid)
		return nil
	})
}

func (u *GenericUpstream) Stop() {
	u.upstreamCtx.mainLifecycle.Stop()
	u.upstreamCtx.cancelFunc()
	u.processorAggregator.StopProcessor(event_processors.SettingsValidatorProcessorType)
	u.PartialStop()

	for _, connector := range u.apiConnectors {
		connector.Stop()
	}
}

func (u *GenericUpstream) Running() bool {
	return u.upstreamCtx.mainLifecycle.Running()
}

func (u *GenericUpstream) PartialStop() {
	u.processorAggregator.StopProcessor(event_processors.BlockEventProcessorType)
	u.processorAggregator.StopProcessor(event_processors.HealthValidatorProcessorType)
	u.processorAggregator.StopProcessor(event_processors.LowerBoundEventProcessorType)
	u.processorAggregator.StopProcessor(event_processors.HeadEventProcessorType)
	u.processorAggregator.StopProcessor(event_processors.LabelsProcessorType)
	u.processorAggregator.StopProcessor(event_processors.CapEventProcessorType)
	u.processorAggregator.StopProcessor(event_processors.MethodsEventProcessorType)
}

func (u *GenericUpstream) Resume() {
	u.processorAggregator.StartProcessor(event_processors.HeadEventProcessorType)
	u.processorAggregator.StartProcessor(event_processors.BlockEventProcessorType)
	u.processorAggregator.StartProcessor(event_processors.HealthValidatorProcessorType)
	u.processorAggregator.StartProcessor(event_processors.LowerBoundEventProcessorType)
	u.processorAggregator.StartProcessor(event_processors.LabelsProcessorType)
	u.processorAggregator.StartProcessor(event_processors.CapEventProcessorType)
	u.processorAggregator.StartProcessor(event_processors.MethodsEventProcessorType)
}

func (u *GenericUpstream) Subscribe(name string) *utils.Subscription[protocol.UpstreamEvent] {
	return u.subManager.Subscribe(name)
}

func (u *GenericUpstream) GetUpstreamState() protocol.UpstreamState {
	return u.upstreamState.Load()
}

func (u *GenericUpstream) GetVendorType() UpstreamVendor {
	return u.vendorType
}

func (u *GenericUpstream) UpdateHead(height, slot uint64) {
	u.processorAggregator.UpdateHead(event_processors.NewHeadUpdateData(height, slot))
}

func (u *GenericUpstream) UpdateBlock(block protocol.Block, blockType protocol.BlockType) {
	u.processorAggregator.UpdateBlock(event_processors.NewGenericBlockUpdateData(block, blockType))
}

func (u *GenericUpstream) UpdateLowerBound(data protocol.LowerBoundData) {
	u.emitter(&protocol.LowerBoundUpstreamStateEvent{Data: data})
}

func (u *GenericUpstream) UpdateHeadLag(lag int64) {
	if lag < 0 {
		lag = 0
	}
	u.headLag.Store(lag)
	u.emitter(&protocol.StatusUpstreamStateEvent{Lag: new(lag)})
}

func (u *GenericUpstream) BanMethod(method string) {
	u.emitter(&protocol.BanMethodUpstreamStateEvent{Method: method})
}

func (u *GenericUpstream) GetConnector(connectorType specs.ApiConnectorType) connectors.ApiConnector {
	connector, _ := lo.Find(u.apiConnectors, func(item connectors.ApiConnector) bool {
		return item.GetType() == connectorType
	})
	return connector
}

func (u *GenericUpstream) GetHashIndex() string {
	return u.upstreamIndexHex
}

// newUpstreamMethods rebuilds the upstream's method set from the chain spec. Composition
// order is spec - unsupported - config disable - banned + config enable:
// methods.NewUpstreamMethods removes everything disabled before adding anything enabled,
// so putting both runtime subtractions in DisableMethods leaves config enable as the last
// word - the same precedence the ban path grants it.
//
// bannedMethods and unsupportedMethods stay separate sets in the caller: an unban must
// restore only what a ban took away, never what detection found missing.
func (u *GenericUpstream) newUpstreamMethods(bannedMethods, unsupportedMethods mapset.Set[string]) methods.Methods {
	newConfig := &config.MethodsConfig{
		EnableMethods:  u.upConfig.Methods.EnableMethods,
		DisableMethods: lo.Union(bannedMethods.ToSlice(), unsupportedMethods.ToSlice(), u.upConfig.Methods.DisableMethods),
	}
	connectorTypes := lo.Map(u.apiConnectors, func(item connectors.ApiConnector, index int) specs.ApiConnectorType {
		return item.GetType()
	})
	newMethods, _ := methods.NewUpstreamMethods(chains.GetMethodSpecNameByChain(u.configuredChain.Chain), newConfig, connectorTypes)
	return newMethods
}

func (u *GenericUpstream) startConnectors(_ context.Context) {
	// Capabilities derived from connector state (WsCap, NewHeads/Logs, PendingTx) are
	// now produced by the cap pipeline (caps.CapProcessor + CapDetectors), which
	// subscribes to the connectors' state streams itself. Here we only start them.
	for _, connector := range u.apiConnectors {
		connector.Start()
	}
}
