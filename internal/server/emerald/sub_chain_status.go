package emerald

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/buildinfo"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

var errNilUpstreamSupervisor = errors.New("upstream supervisor cannot be nil")

// The chain-status protocol is delta-based, and deltas travel over lossy hops
// (buffered-channel fan-outs on both ends drop events under pressure). A lost
// delta used to leave the subscriber permanently stale until it rebuilt the
// connection. The periodic resync bounds that staleness by one interval.
const defaultChainStateResyncInterval = time.Minute

// chainStatusSubs holds the per-chain subscriptions so they are released together.
type chainStatusSubs struct {
	state  *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent]
	groups *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] // nil when separation is off
}

func SubscribeChainStatus(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeChainStatusServer,
	separation bool,
) error {
	return SubscribeChainStatusWithResync(upstreamSupervisor, stream, defaultChainStateResyncInterval, separation)
}

// SubscribeChainStatusWithResync is SubscribeChainStatus with a caller-chosen
// state-resync interval; tests use it to shrink the wait.
func SubscribeChainStatusWithResync(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeChainStatusServer,
	resyncInterval time.Duration,
	separation bool,
) error {
	if upstreamSupervisor == nil {
		return errNilUpstreamSupervisor
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	responses := make(chan *dshackle.SubscribeChainStatusResponse, 100)
	chainSubs := make(map[chains.Chain]chainStatusSubs)
	chainSupervisorEventsSub := upstreamSupervisor.SubscribeChainSupervisor(fmt.Sprintf("chain_status_%s", uuid.NewString()))
	defer func() {
		chainSupervisorEventsSub.Unsubscribe()
		for _, subs := range chainSubs {
			subs.state.Unsubscribe()
			if subs.groups != nil {
				subs.groups.Unsubscribe()
			}
		}
	}()

	for _, chainSupervisor := range upstreamSupervisor.GetChainSupervisors() {
		subscribeChainSupervisorStates(ctx, chainSupervisor, chainSubs, responses, resyncInterval, separation)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case chainSupervisorEvent, ok := <-chainSupervisorEventsSub.Events:
			if ok {
				switch c := chainSupervisorEvent.(type) {
				case *upstreams.AddChainSupervisorEvent:
					subscribeChainSupervisorStates(ctx, c.ChainSupervisor, chainSubs, responses, resyncInterval, separation)
				}
			}
		case response, ok := <-responses:
			if ok {
				if err := stream.Send(response); err != nil {
					log.Error().Err(err).Msgf("failed to send a SubscribeChainStatusResponse")
					return err
				}
			}
		}
	}
}

func subscribeChainSupervisorStates(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	chainSubs map[chains.Chain]chainStatusSubs,
	responses chan *dshackle.SubscribeChainStatusResponse,
	resyncInterval time.Duration,
	separation bool,
) {
	if chainSupervisor == nil {
		return
	}
	if _, exists := chainSubs[chainSupervisor.GetChain()]; exists {
		return
	}

	chainSupervisorStatesSub := chainSupervisor.SubscribeState(
		fmt.Sprintf("chain_supervisor_states_%s_%s", chainSupervisor.GetChain(), uuid.NewString()),
	)
	subs := chainStatusSubs{state: chainSupervisorStatesSub}
	// stays nil when separation is off, so the select case below never fires
	var groupEvents chan *upstreams.ChainSupervisorStateWrapperEvent
	if separation {
		groupsSub := chainSupervisor.SubscribeNodeGroupStates(
			fmt.Sprintf("chain_supervisor_group_states_%s_%s", chainSupervisor.GetChain(), uuid.NewString()),
		)
		subs.groups = groupsSub
		groupEvents = groupsSub.Events
	}
	chainSubs[chainSupervisor.GetChain()] = subs
	configChain := chains.GetChain(chainSupervisor.GetChain().String())
	grpcId := configChain.GrpcId

	go func() {
		// we should wait for the head before sending the very first event
		fullSent := false
		groupFullSent := make(map[string]bool)

		state := chainSupervisor.GetChainState()
		if !state.HeadData.IsEmpty() {
			if !sendResponse(ctx, responses, toFullResponse(grpcId, state)) {
				return
			}
			fullSent = true
			if separation && !sendGroupFulls(ctx, chainSupervisor, responses, grpcId, groupFullSent) {
				return
			}
		}

		resyncTicker := time.NewTicker(resyncInterval)
		defer resyncTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-resyncTicker.C:
				// Nothing to resync until the initial full response went out:
				// the consumer creates its per-chain object only from a full
				// response and silently skips state updates before that.
				if !fullSent {
					continue
				}
				state = chainSupervisor.GetChainState()
				if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, "", snapshotStateWrappers(state))) {
					return
				}
				if separation && !resyncGroups(ctx, chainSupervisor, responses, grpcId, groupFullSent) {
					return
				}
			case event, ok := <-chainSupervisorStatesSub.Events:
				if ok {
					if len(event.Wrappers) == 0 {
						continue
					}
					state = chainSupervisor.GetChainState()
					// ignore all the events before getting a head, then send a full event first
					if !fullSent {
						if state.HeadData.IsEmpty() {
							continue
						}
						if !sendResponse(ctx, responses, toFullResponse(grpcId, state)) {
							return
						}
						fullSent = true
						if separation && !sendGroupFulls(ctx, chainSupervisor, responses, grpcId, groupFullSent) {
							return
						}
						continue
					}
					if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, "", event.Wrappers)) {
						return
					}
				}
			case event, ok := <-groupEvents:
				if ok {
					if len(event.Wrappers) == 0 || event.NodeGroupId == "" {
						continue
					}
					// the network full always goes out first; the groups known by
					// then are announced right after it
					if !fullSent {
						continue
					}
					if !groupFullSent[event.NodeGroupId] {
						// same head gating as the network level; a group that
						// died before it was ever announced is skipped entirely
						groupState, exists := chainSupervisor.GetNodeGroupState(event.NodeGroupId)
						if !exists || groupState.HeadData.IsEmpty() {
							continue
						}
						if !sendResponse(ctx, responses, toGroupFullResponse(grpcId, event.NodeGroupId, groupState)) {
							return
						}
						groupFullSent[event.NodeGroupId] = true
						continue
					}
					if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, event.NodeGroupId, event.Wrappers)) {
						return
					}
				}
			}
		}
	}()
}

// sendGroupFulls announces every live group that has a head and wasn't
// announced yet; the rest are announced by their deltas or the resync.
func sendGroupFulls(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
	grpcId int,
	groupFullSent map[string]bool,
) bool {
	for nodeGroupId, groupState := range chainSupervisor.GetNodeGroupStates() {
		if groupFullSent[nodeGroupId] || groupState.HeadData.IsEmpty() {
			continue
		}
		if !sendResponse(ctx, responses, toGroupFullResponse(grpcId, nodeGroupId, groupState)) {
			return false
		}
		groupFullSent[nodeGroupId] = true
	}
	return true
}

// resyncGroups snapshots every announced group and reconciles groupFullSent:
// dropped groups are purged so a re-formed group is re-announced with a full,
// live groups whose announce delta was lost are announced now.
func resyncGroups(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
	grpcId int,
	groupFullSent map[string]bool,
) bool {
	live := chainSupervisor.GetNodeGroupStates()
	for nodeGroupId := range groupFullSent {
		if _, ok := live[nodeGroupId]; !ok {
			delete(groupFullSent, nodeGroupId)
		}
	}
	for nodeGroupId, groupState := range live {
		if !groupFullSent[nodeGroupId] {
			if groupState.HeadData.IsEmpty() {
				continue
			}
			if !sendResponse(ctx, responses, toGroupFullResponse(grpcId, nodeGroupId, groupState)) {
				return false
			}
			groupFullSent[nodeGroupId] = true
			continue
		}
		if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, nodeGroupId, snapshotStateWrappers(groupState))) {
			return false
		}
	}
	return true
}

func sendResponse(
	ctx context.Context,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
	resp *dshackle.SubscribeChainStatusResponse,
) bool {
	// nil = nothing to send (e.g. a caps-only delta), not a failure
	if resp == nil {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case responses <- resp:
		return true
	}
}

// snapshotStateWrappers rebuilds the full current state as a wrapper list,
// deliberately WITHOUT the head. Head freshness is already guaranteed by the
// per-block head events; more importantly, consumers reduce any response that
// carries a head to a head-only update for an existing upstream, so a
// snapshot with a head would lose exactly the state it is meant to repair.
func snapshotStateWrappers(state upstreams.ChainSupervisorState) []upstreams.ChainSupervisorStateWrapper {
	return []upstreams.ChainSupervisorStateWrapper{
		upstreams.NewStatusWrapper(state.Status),
		upstreams.NewMethodsWrapper(state.Methods.GetSupportedMethods().ToSlice()),
		upstreams.NewLowerBoundsWrapper(lo.Values(state.LowerBounds)),
		upstreams.NewBlocksWrapper(state.Blocks),
		upstreams.NewSubMethodsWrapper(state.SubMethods.ToSlice()),
		upstreams.NewLabelsWrapper(state.ChainLabels),
	}
}

// stateWrappersToResponse returns nil when no wrapper maps to a wire event
// (e.g. a caps-only delta) - sending a ChainDescription with an empty event
// would only confuse consumers.
func stateWrappersToResponse(grpcId int, nodeGroupId string, wrappers []upstreams.ChainSupervisorStateWrapper) *dshackle.SubscribeChainStatusResponse {
	events := make([]*dshackle.ChainEvent, 0, len(wrappers))

	for _, wrapper := range wrappers {
		switch w := wrapper.(type) {
		case *upstreams.HeadWrapper:
			events = append(events, HeadToApi(w.Head))
		case *upstreams.BlocksWrapper:
			events = append(events, BlocksToApi(w.Blocks))
		case *upstreams.MethodsWrapper:
			events = append(events, SupportedMethodsToApi(w.Methods))
		case *upstreams.StatusWrapper:
			events = append(events, ChainStatusToApi(w.Status))
		case *upstreams.LowerBoundsWrapper:
			events = append(events, LowerBoundsToApi(w.LowerBounds))
		case *upstreams.LabelsWrapper:
			events = append(events, LabelsToApi(w.Labels))
		case *upstreams.SubMethodsWrapper:
			events = append(events, SubMethodsToApi(w.SubMethods))
		case *upstreams.CapsWrapper:
			// caps drive the local sub-engines only; there is no wire event for them
		}
	}
	if len(events) == 0 {
		return nil
	}

	return &dshackle.SubscribeChainStatusResponse{
		ChainDescription: &dshackle.ChainDescription{
			Chain:       dshackle.ChainRef(grpcId),
			ChainEvent:  events,
			NodeGroupId: nodeGroupId,
		},
	}
}

func fullStateEvents(state upstreams.ChainSupervisorState) []*dshackle.ChainEvent {
	return []*dshackle.ChainEvent{
		ChainStatusToApi(state.Status),
		SupportedMethodsToApi(state.Methods.GetSupportedMethods().ToSlice()),
		LowerBoundsToApi(lo.Values(state.LowerBounds)),
		HeadToApi(state.HeadData.Head),
		BlocksToApi(state.Blocks),
		SubMethodsToApi(state.SubMethods.ToSlice()),
		LabelsToApi(state.ChainLabels),
	}
}

func toFullResponse(grpcId int, state upstreams.ChainSupervisorState) *dshackle.SubscribeChainStatusResponse {
	return &dshackle.SubscribeChainStatusResponse{
		ChainDescription: &dshackle.ChainDescription{
			Chain:      dshackle.ChainRef(grpcId),
			ChainEvent: fullStateEvents(state),
		},
		BuildInfo: &dshackle.BuildInfo{
			Version: buildinfo.ProductVersion(),
		},
		FullResponse: true,
	}
}

// toGroupFullResponse carries the same event set as toFullResponse; BuildInfo
// describes the server and stays on the network full only.
func toGroupFullResponse(grpcId int, nodeGroupId string, state upstreams.ChainSupervisorState) *dshackle.SubscribeChainStatusResponse {
	return &dshackle.SubscribeChainStatusResponse{
		ChainDescription: &dshackle.ChainDescription{
			Chain:       dshackle.ChainRef(grpcId),
			ChainEvent:  fullStateEvents(state),
			NodeGroupId: nodeGroupId,
		},
		FullResponse: true,
	}
}
