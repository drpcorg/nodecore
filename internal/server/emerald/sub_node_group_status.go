package emerald

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/google/uuid"
)

// SubscribeNodeGroupStatus streams chain status per node group instead of the
// merged per-network view: a head- and status-gated full response per live
// group, then group-scoped deltas, with the same periodic resync. A group's
// ChainStatus going unavailable is its removal signal.
func SubscribeNodeGroupStatus(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeNodeGroupStatusServer,
) error {
	return SubscribeNodeGroupStatusWithResync(upstreamSupervisor, stream, defaultChainStateResyncInterval)
}

// SubscribeNodeGroupStatusWithResync is SubscribeNodeGroupStatus with a
// caller-chosen state-resync interval; tests use it to shrink the wait.
func SubscribeNodeGroupStatusWithResync(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeNodeGroupStatusServer,
	resyncInterval time.Duration,
) error {
	return streamChainStatuses(upstreamSupervisor, stream, resyncInterval, subscribeNodeGroupStates)
}

func subscribeNodeGroupStates(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	chainSubs map[chains.Chain]*utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent],
	responses chan *dshackle.SubscribeChainStatusResponse,
	resyncInterval time.Duration,
) {
	if chainSupervisor == nil {
		return
	}
	if _, exists := chainSubs[chainSupervisor.GetChain()]; exists {
		return
	}

	groupStatesSub := chainSupervisor.SubscribeNodeGroupStates(
		fmt.Sprintf("chain_supervisor_group_states_%s_%s", chainSupervisor.GetChain(), uuid.NewString()),
	)
	chainSubs[chainSupervisor.GetChain()] = groupStatesSub
	configChain := chains.GetChain(chainSupervisor.GetChain().String())
	grpcId := configChain.GrpcId

	go func() {
		// groupFullSent tracks what the consumer knows: a group is in the map
		// while the consumer holds an object for it. Whenever the stream tells
		// the consumer a group is unavailable (delta, snapshot, or tombstone)
		// the consumer drops that object, so the id must leave the map and the
		// group's next appearance must be a fresh full response.
		groupFullSent := make(map[string]bool)

		if !syncGroups(ctx, chainSupervisor, responses, grpcId, groupFullSent) {
			return
		}

		resyncTicker := time.NewTicker(resyncInterval)
		defer resyncTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-resyncTicker.C:
				if !syncGroups(ctx, chainSupervisor, responses, grpcId, groupFullSent) {
					return
				}
			case event, ok := <-groupStatesSub.Events:
				if ok {
					if len(event.Wrappers) == 0 || event.NodeGroupId == "" {
						continue
					}
					if !groupFullSent[event.NodeGroupId] {
						// a group that died before it was ever announced is skipped entirely
						groupState, exists := chainSupervisor.GetNodeGroupState(event.NodeGroupId)
						if !exists {
							continue
						}
						if !announceGroup(ctx, responses, grpcId, event.NodeGroupId, groupState, groupFullSent) {
							return
						}
						continue
					}
					if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, event.NodeGroupId, event.Wrappers)) {
						return
					}
					// a forwarded Unavailable is the removal signal - the consumer
					// just dropped the group, so a comeback needs a fresh full
					if hasUnavailableStatus(event.Wrappers) {
						delete(groupFullSent, event.NodeGroupId)
					}
				}
			}
		}
	}()
}

// announceGroup introduces a group to the subscriber with a full response.
// Head-gated like the network-level stream; unavailable groups are not
// announced - for consumers Unavailable means "removed", so such a group is
// introduced once it recovers.
func announceGroup(
	ctx context.Context,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
	grpcId int,
	nodeGroupId string,
	groupState upstreams.ChainSupervisorState,
	groupFullSent map[string]bool,
) bool {
	if groupState.HeadData.IsEmpty() || groupState.Status == protocol.Unavailable {
		return true
	}
	if !sendResponse(ctx, responses, toFullResponse(grpcId, nodeGroupId, groupState)) {
		return false
	}
	groupFullSent[nodeGroupId] = true
	return true
}

// syncGroups reconciles the subscriber's group view with the live set:
// announces groups not shown yet, snapshots the announced ones, and sends a
// tombstone for announced groups that no longer exist - so a lost removal
// delta is repaired within one resync interval, like any other lost delta.
func syncGroups(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
	grpcId int,
	groupFullSent map[string]bool,
) bool {
	live := chainSupervisor.GetNodeGroupStates()
	for nodeGroupId := range groupFullSent {
		if _, ok := live[nodeGroupId]; !ok {
			tombstone := []upstreams.ChainSupervisorStateWrapper{upstreams.NewStatusWrapper(protocol.Unavailable)}
			if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, nodeGroupId, tombstone)) {
				return false
			}
			delete(groupFullSent, nodeGroupId)
		}
	}
	for nodeGroupId, groupState := range live {
		if !groupFullSent[nodeGroupId] {
			if !announceGroup(ctx, responses, grpcId, nodeGroupId, groupState, groupFullSent) {
				return false
			}
			continue
		}
		if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, nodeGroupId, snapshotStateWrappers(groupState))) {
			return false
		}
		// an unavailable snapshot removes the group on the consumer just like
		// the delta does
		if groupState.Status == protocol.Unavailable {
			delete(groupFullSent, nodeGroupId)
		}
	}
	return true
}

func hasUnavailableStatus(wrappers []upstreams.ChainSupervisorStateWrapper) bool {
	for _, wrapper := range wrappers {
		if statusWrapper, ok := wrapper.(*upstreams.StatusWrapper); ok && statusWrapper.Status == protocol.Unavailable {
			return true
		}
	}
	return false
}
