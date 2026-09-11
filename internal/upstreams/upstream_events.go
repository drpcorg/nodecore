package upstreams

import (
	"context"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/rs/zerolog/log"
)

// update upstream state through one pipeline
func (u *GenericUpstream) processStateEvents(ctx context.Context, initialValid bool) {
	bannedMethods := mapset.NewThreadUnsafeSet[string]()
	// unsupportedMethods is what method detection last reported. It is tracked separately
	// from bannedMethods so that an unban - which fires on a timer - can never restore a
	// method the node structurally lacks.
	unsupportedMethods := mapset.NewThreadUnsafeSet[string]()
	methodSpecName := chains.GetMethodSpecNameByChain(u.configuredChain.Chain)
	forceEnabled := func(method string) bool {
		return methods.IsForceEnabled(u.upConfig.Methods, specs.GetSpecMethodWithFallback(methodSpecName, method))
	}
	validUpstream := initialValid
	// baseAvail is the availability reported by health probes (setStatus),
	// tracked here rather than in the shared UpstreamState because only the
	// derived effective availability is of interest to consumers. It is combined
	// with the upstream's current head lag (u.headLag) to derive the effective
	// availability published on UpstreamState.Status.
	baseAvail := u.upstreamState.Load().Status
	// headPaused tracks whether toggleHeadOnSyncing has stopped the head processor
	headPaused := false
	for {
		select {
		case <-ctx.Done():
			log.Info().Msgf("stopping upstream '%s' event processing", u.id)
			return
		case event := <-u.stateChan:
			state := u.upstreamState.Load()
			var eventType protocol.UpstreamEventType = &protocol.StateUpstreamEvent{State: &state}

			switch stateEvent := event.(type) {
			case *protocol.FatalErrorUpstreamStateEvent:
				if !validUpstream {
					continue
				}
				log.Warn().Msgf("upstream '%s' settings are invalid, it will be stopped", u.id)
				eventType = &protocol.RemoveUpstreamEvent{}
				validUpstream = false
				// PartialStop takes the head down with everything else and Resume brings it
				// back outside this loop, so the pause bookkeeping starts over
				headPaused = false
				u.publishUpstreamEvent(state, eventType)
			case *protocol.ValidUpstreamStateEvent:
				if validUpstream {
					continue
				}
				log.Warn().Msgf("upstream '%s' settings are valid", u.id)
				eventType = &protocol.ValidUpstreamEvent{State: &state}
				validUpstream = true
			case *protocol.BanMethodUpstreamStateEvent:
				// A ban the config enables away is not worth recording: it would leave the
				// method enabled, fire a pointless unban later, and re-arm on the next
				// failure. This sits on a per-request path - MethodBanHook fires for every
				// failing response - so it must stay cheap and silent.
				if bannedMethods.ContainsOne(stateEvent.Method) || forceEnabled(stateEvent.Method) {
					continue
				}
				time.AfterFunc(u.upConfig.Methods.BanDuration, func() {
					u.emitter(&protocol.UnbanMethodUpstreamStateEvent{Method: stateEvent.Method})
				})
				log.Warn().Msgf("the method %s has been banned on upstream %s", stateEvent.Method, u.id)
				bannedMethods.Add(stateEvent.Method)
				state.UpstreamMethods = u.newUpstreamMethods(bannedMethods, unsupportedMethods)
			case *protocol.UnbanMethodUpstreamStateEvent:
				if !bannedMethods.ContainsOne(stateEvent.Method) {
					continue
				}
				log.Warn().Msgf("the method %s has been unbanned on upstream %s", stateEvent.Method, u.id)
				bannedMethods.Remove(stateEvent.Method)
				state.UpstreamMethods = u.newUpstreamMethods(bannedMethods, unsupportedMethods)
			case *protocol.UnsupportedMethodsUpstreamStateEvent:
				if unsupportedMethods.Equal(stateEvent.Methods) {
					continue
				}
				unsupportedMethods = stateEvent.Methods.Clone()
				for _, method := range unsupportedMethods.ToSlice() {
					if forceEnabled(method) {
						log.Warn().Msgf(
							"method %s is not supported by upstream %s but stays enabled because the config force-enables it",
							method, u.id,
						)
					}
				}
				state.UpstreamMethods = u.newUpstreamMethods(bannedMethods, unsupportedMethods)
			case *protocol.StatusUpstreamStateEvent:
				if !validUpstream {
					continue
				}
				if stateEvent.Lag == nil {
					baseAvail = stateEvent.Status
					headPaused = u.toggleHeadOnSyncing(baseAvail, headPaused)
				}
				newAvail := protocol.StatusByLag(u.headLag.Load(), baseAvail, u.configuredChain.Settings.Lags.Syncing)
				if newAvail != state.Status {
					state.Status = newAvail
					u.publishUpstreamEvent(state, eventType)
				}
				continue
			case *protocol.HeadUpstreamStateEvent:
				state = stateEvent.ProcessEvent(state)
				eventType = &protocol.HeadUpstreamEvent{Status: state.Status, Head: state.HeadData}
			default:
				if stateEvent.Same(state) {
					continue
				}
				state = stateEvent.ProcessEvent(state)
			}

			if validUpstream {
				u.publishUpstreamEvent(state, eventType)
			}
		}
	}
}

// toggleHeadOnSyncing stops the head processor when the health probes report Syncing
// and starts it again on the first non-Syncing verdict. Only probe results reach here:
// lag-observer events carry a Lag and never change baseAvail, so a node that merely
// fell behind keeps its head. Returns the new paused state.
func (u *GenericUpstream) toggleHeadOnSyncing(probeAvail protocol.AvailabilityStatus, paused bool) bool {
	if !u.pauseHeadWhileSyncing {
		return false
	}
	syncing := probeAvail == protocol.Syncing
	switch {
	case syncing && !paused:
		log.Warn().Msgf("upstream '%s' reports syncing, pausing its head", u.id)
		u.processorAggregator.StopProcessor(event_processors.HeadEventProcessorType)
	case !syncing && paused:
		log.Warn().Msgf("upstream '%s' is synced again, resuming its head", u.id)
		u.processorAggregator.StartProcessor(event_processors.HeadEventProcessorType)
	}
	return syncing
}

func (u *GenericUpstream) createUpstreamEvent(eventType protocol.UpstreamEventType) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id:        u.id,
		Chain:     u.configuredChain.Chain,
		EventType: eventType,
	}
}

func (u *GenericUpstream) publishUpstreamEvent(state protocol.UpstreamState, eventType protocol.UpstreamEventType) {
	u.upstreamState.Store(state)
	upstreamEvent := u.createUpstreamEvent(eventType)

	u.subManager.Publish(upstreamEvent)
}
