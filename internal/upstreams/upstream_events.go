package upstreams

import (
	"context"
	"slices"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// update upstream state through one pipeline
func (u *BaseUpstream) processStateEvents(ctx context.Context, initialValid bool) {
	bannedMethods := mapset.NewThreadUnsafeSet[string]()
	validUpstream := initialValid
	// noFinality chains never publish a BlockUpstreamStateEvent for the
	// finalized block (there isn't one) and the health validator emits
	// StatusUpstreamStateEvent{Status: Available}, which deduplicates against
	// the initial state. Without anything publishing a StateUpstreamEvent the
	// chain supervisor never registers the upstream and the router cannot
	// route requests. Force a one-shot StateUpstreamEvent piggyback on the
	// first head publication so the supervisor learns the upstream exists.
	noFinality := chains.IsNoFinalityChain(u.chain)
	stateBroadcast := false
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
				u.publishUpstreamEvent(state, eventType)
			case *protocol.ValidUpstreamStateEvent:
				if validUpstream {
					continue
				}
				log.Warn().Msgf("upstream '%s' settings are valid", u.id)
				eventType = &protocol.ValidUpstreamEvent{}
				validUpstream = true
			case *protocol.BanMethodUpstreamStateEvent:
				if bannedMethods.ContainsOne(stateEvent.Method) || slices.Contains(u.upConfig.Methods.EnableMethods, stateEvent.Method) {
					continue
				}
				time.AfterFunc(u.upConfig.Methods.BanDuration, func() {
					u.emitter(&protocol.UnbanMethodUpstreamStateEvent{Method: stateEvent.Method})
				})
				log.Warn().Msgf("the method %s has been banned on upstream %s", stateEvent.Method, u.id)
				bannedMethods.Add(stateEvent.Method)
				state.UpstreamMethods = u.newUpstreamMethods(bannedMethods)
			case *protocol.UnbanMethodUpstreamStateEvent:
				if !bannedMethods.ContainsOne(stateEvent.Method) {
					continue
				}
				log.Warn().Msgf("the method %s has been unbanned on upstream %s", stateEvent.Method, u.id)
				bannedMethods.Remove(stateEvent.Method)
				state.UpstreamMethods = u.newUpstreamMethods(bannedMethods)
			case *protocol.HeadUpstreamStateEvent:
				state = stateEvent.ProcessEvent(state)
				eventType = &protocol.HeadUpstreamEvent{Status: state.Status, Head: state.HeadData}
				if noFinality && !stateBroadcast && validUpstream {
					u.publishUpstreamEvent(state, &protocol.StateUpstreamEvent{State: &state})
					stateBroadcast = true
				}
			default:
				if stateEvent.Same(state) {
					continue
				}
				state = stateEvent.ProcessEvent(state)
			}

			if validUpstream {
				u.publishUpstreamEvent(state, eventType)
				if _, isState := eventType.(*protocol.StateUpstreamEvent); isState {
					stateBroadcast = true
				}
			}
		}
	}
}

func (u *BaseUpstream) createUpstreamEvent(eventType protocol.UpstreamEventType) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id:        u.id,
		Chain:     u.chain,
		EventType: eventType,
	}
}

func (u *BaseUpstream) publishUpstreamEvent(state protocol.UpstreamState, eventType protocol.UpstreamEventType) {
	u.upstreamState.Store(state)
	upstreamEvent := u.createUpstreamEvent(eventType)

	u.subManager.Publish(upstreamEvent)
}
