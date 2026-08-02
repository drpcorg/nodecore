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

// chainStatusStream is the server side of both status RPCs - they share the
// response message, so the streaming scaffold is shared too.
type chainStatusStream interface {
	Send(*dshackle.SubscribeChainStatusResponse) error
	Context() context.Context
}

// subscribeChainFunc starts the per-chain producer goroutine of one RPC flavor.
type subscribeChainFunc func(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	chainSubs map[chains.Chain]*utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent],
	responses chan *dshackle.SubscribeChainStatusResponse,
	resyncInterval time.Duration,
)

func SubscribeChainStatus(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeChainStatusServer,
) error {
	return SubscribeChainStatusWithResync(upstreamSupervisor, stream, defaultChainStateResyncInterval)
}

// SubscribeChainStatusWithResync is SubscribeChainStatus with a caller-chosen
// state-resync interval; tests use it to shrink the wait.
func SubscribeChainStatusWithResync(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeChainStatusServer,
	resyncInterval time.Duration,
) error {
	return streamChainStatuses(upstreamSupervisor, stream, resyncInterval, subscribeChainSupervisorStates)
}

func streamChainStatuses(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream chainStatusStream,
	resyncInterval time.Duration,
	subscribeChain subscribeChainFunc,
) error {
	if upstreamSupervisor == nil {
		return errNilUpstreamSupervisor
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	responses := make(chan *dshackle.SubscribeChainStatusResponse, 100)
	chainSubs := make(map[chains.Chain]*utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent])
	chainSupervisorEventsSub := upstreamSupervisor.SubscribeChainSupervisor(fmt.Sprintf("chain_status_%s", uuid.NewString()))
	defer func() {
		chainSupervisorEventsSub.Unsubscribe()
		for _, sub := range chainSubs {
			sub.Unsubscribe()
		}
	}()

	for _, chainSupervisor := range upstreamSupervisor.GetChainSupervisors() {
		subscribeChain(ctx, chainSupervisor, chainSubs, responses, resyncInterval)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case chainSupervisorEvent, ok := <-chainSupervisorEventsSub.Events:
			if ok {
				switch c := chainSupervisorEvent.(type) {
				case *upstreams.AddChainSupervisorEvent:
					subscribeChain(ctx, c.ChainSupervisor, chainSubs, responses, resyncInterval)
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

	chainSupervisorStatesSub := chainSupervisor.SubscribeState(
		fmt.Sprintf("chain_supervisor_states_%s_%s", chainSupervisor.GetChain(), uuid.NewString()),
	)
	chainSubs[chainSupervisor.GetChain()] = chainSupervisorStatesSub
	configChain := chains.GetChain(chainSupervisor.GetChain().String())
	grpcId := configChain.GrpcId

	go func() {
		// we should wait for the head before sending the very first event
		fullSent := false

		state := chainSupervisor.GetChainState()
		if !state.HeadData.IsEmpty() {
			if !sendResponse(ctx, responses, toFullResponse(grpcId, "", state)) {
				return
			}
			fullSent = true
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
						if !sendResponse(ctx, responses, toFullResponse(grpcId, "", state)) {
							return
						}
						fullSent = true
						continue
					}
					if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, "", event.Wrappers)) {
						return
					}
				}
			}
		}
	}()
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

func toFullResponse(grpcId int, nodeGroupId string, state upstreams.ChainSupervisorState) *dshackle.SubscribeChainStatusResponse {
	return &dshackle.SubscribeChainStatusResponse{
		ChainDescription: &dshackle.ChainDescription{
			Chain:       dshackle.ChainRef(grpcId),
			ChainEvent:  fullStateEvents(state),
			NodeGroupId: nodeGroupId,
		},
		BuildInfo: &dshackle.BuildInfo{
			Version: buildinfo.ProductVersion(),
		},
		FullResponse: true,
	}
}
