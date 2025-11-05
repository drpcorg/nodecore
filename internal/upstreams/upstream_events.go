package upstreams

import (
	"context"
	"fmt"
	"slices"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/rs/zerolog/log"
)

// update upstream state through one pipeline
func (u *Upstream) processStateEvents(ctx context.Context) {
	bannedMethods := mapset.NewThreadUnsafeSet[string]()
	validUpstream := true
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("stopping upstream event processing")
			return
		case event := <-u.stateChan:
			state := u.upstreamState.Load()
			var eventType protocol.UpstreamEventType = &protocol.StateUpstreamEvent{State: &state}

			switch stateEvent := event.(type) {
			case *protocol.FatalErrorUpstreamStateEvent:
				eventType = &protocol.RemoveUpstreamEvent{}
				validUpstream = false
				u.publishUpstreamEvent(state, eventType)
			case *protocol.ValidUpstreamStateEvent:
				eventType = &protocol.ValidUpstreamEvent{}
				validUpstream = true
			case *protocol.HeadUpstreamStateEvent:
				if state.HeadData != nil && state.HeadData.IsEmpty() {
					state.Status = protocol.Available
				}
				state.HeadData = stateEvent.HeadData
			case *protocol.BlockUpstreamStateEvent:
				state.BlockInfo.AddBlock(stateEvent.BlockData, stateEvent.BlockType)
			case *protocol.BanMethodUpstreamStateEvent:
				if bannedMethods.ContainsOne(stateEvent.Method) || slices.Contains(u.upConfig.Methods.EnableMethods, stateEvent.Method) {
					continue
				}
				time.AfterFunc(u.upConfig.Methods.BanDuration, func() {
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

			if validUpstream {
				u.publishUpstreamEvent(state, eventType)
			}
		}
	}
}

func (u *Upstream) createUpstreamEvent(eventType protocol.UpstreamEventType) protocol.UpstreamEvent {
	return protocol.UpstreamEvent{
		Id:        u.Id,
		Chain:     u.Chain,
		EventType: eventType,
	}
}

func (u *Upstream) publishUpstreamEvent(state protocol.UpstreamState, eventType protocol.UpstreamEventType) {
	u.upstreamState.Store(state)
	upstreamEvent := u.createUpstreamEvent(eventType)

	u.subManager.Publish(upstreamEvent)
}
