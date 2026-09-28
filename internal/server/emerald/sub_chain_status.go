package emerald

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/buildinfo"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/drpcorg/public/pkg/dshackle"
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

// chainStatusProducer subscribes to one chain supervisor and starts the
// goroutine that turns its events into responses.
type chainStatusProducer func(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent]

// statusSink is how one RPC flavor writes the responses of its producers.
type statusSink interface {
	write(response *dshackle.SubscribeChainStatusResponse) error
	// flushes fires when buffered responses are due; nil for a write-through sink
	flushes() <-chan time.Time
	flush() error
}

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
	return streamChainStatuses(
		stream.Context(),
		upstreamSupervisor,
		allChains,
		chainSupervisorStatesProducer(resyncInterval),
		chainStatusSender{stream: stream},
	)
}

// streamChainStatuses is the scaffold both status RPCs share: one producer per
// included chain, chains added later included, all feeding one sink.
func streamChainStatuses(
	ctx context.Context,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	includeChain func(chains.Chain) bool,
	produce chainStatusProducer,
	sink statusSink,
) error {
	if upstreamSupervisor == nil {
		return errNilUpstreamSupervisor
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	responses := make(chan *dshackle.SubscribeChainStatusResponse, 100)
	chainSubs := make(map[chains.Chain]*utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent])
	subscribeChain := func(chainSupervisor upstreams.ChainSupervisor) {
		if chainSupervisor == nil || !includeChain(chainSupervisor.GetChain()) {
			return
		}
		if _, exists := chainSubs[chainSupervisor.GetChain()]; exists {
			return
		}
		chainSubs[chainSupervisor.GetChain()] = produce(ctx, chainSupervisor, responses)
	}

	chainSupervisorEventsSub := upstreamSupervisor.SubscribeChainSupervisor(fmt.Sprintf("chain_status_%s", uuid.NewString()))
	defer func() {
		chainSupervisorEventsSub.Unsubscribe()
		for _, sub := range chainSubs {
			sub.Unsubscribe()
		}
	}()

	for _, chainSupervisor := range upstreamSupervisor.GetChainSupervisors() {
		subscribeChain(chainSupervisor)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case chainSupervisorEvent, ok := <-chainSupervisorEventsSub.Events:
			if added, isAdded := chainSupervisorEvent.(*upstreams.AddChainSupervisorEvent); ok && isAdded {
				subscribeChain(added.ChainSupervisor)
			}
		case response, ok := <-responses:
			if ok {
				if err := sink.write(response); err != nil {
					return err
				}
			}
		case <-sink.flushes():
			if err := sink.flush(); err != nil {
				return err
			}
		}
	}
}

func allChains(chains.Chain) bool {
	return true
}

// chainStatusSender sends every response on its own.
type chainStatusSender struct {
	stream dshackle.Blockchain_SubscribeChainStatusServer
}

func (s chainStatusSender) write(response *dshackle.SubscribeChainStatusResponse) error {
	if err := s.stream.Send(response); err != nil {
		log.Error().Err(err).Msgf("failed to send a SubscribeChainStatusResponse")
		return err
	}
	return nil
}

func (s chainStatusSender) flushes() <-chan time.Time {
	return nil
}

func (s chainStatusSender) flush() error {
	return nil
}

func chainSupervisorStatesProducer(resyncInterval time.Duration) chainStatusProducer {
	return func(
		ctx context.Context,
		chainSupervisor upstreams.ChainSupervisor,
		responses chan<- *dshackle.SubscribeChainStatusResponse,
	) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
		chainSupervisorStatesSub := chainSupervisor.SubscribeState(
			fmt.Sprintf("chain_supervisor_states_%s_%s", chainSupervisor.GetChain(), uuid.NewString()),
		)
		grpcId := chains.GetChain(chainSupervisor.GetChain().String()).GrpcId
		go produceChainStates(ctx, chainSupervisor, chainSupervisorStatesSub.Events, responses, grpcId, resyncInterval)
		return chainSupervisorStatesSub
	}
}

func produceChainStates(
	ctx context.Context,
	chainSupervisor upstreams.ChainSupervisor,
	events <-chan *upstreams.ChainSupervisorStateWrapperEvent,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
	grpcId int,
	resyncInterval time.Duration,
) {
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
		case event, ok := <-events:
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
}

func sendResponse(
	ctx context.Context,
	responses chan<- *dshackle.SubscribeChainStatusResponse,
	resp *dshackle.SubscribeChainStatusResponse,
) bool {
	// nil = nothing to send (a caps-only group delta), not a failure
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

// stateWrappersToResponse maps a caps wrapper, which has no wire event, to an
// empty event on the merged stream (its wire format since before groups) and
// drops it on the group stream, which gets nil for a caps-only delta.
func stateWrappersToResponse(grpcId int, nodeGroupId string, wrappers []upstreams.ChainSupervisorStateWrapper) *dshackle.SubscribeChainStatusResponse {
	events := make([]*dshackle.ChainEvent, 0, len(wrappers))
	for _, wrapper := range wrappers {
		if event := wrapperToEvent(wrapper); event != nil || nodeGroupId == "" {
			events = append(events, event)
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

func wrapperToEvent(wrapper upstreams.ChainSupervisorStateWrapper) *dshackle.ChainEvent {
	switch w := wrapper.(type) {
	case *upstreams.HeadWrapper:
		return HeadToApi(w.Head)
	case *upstreams.BlocksWrapper:
		return BlocksToApi(w.Blocks)
	case *upstreams.MethodsWrapper:
		return SupportedMethodsToApi(w.Methods)
	case *upstreams.StatusWrapper:
		return ChainStatusToApi(w.Status)
	case *upstreams.LowerBoundsWrapper:
		return LowerBoundsToApi(w.LowerBounds)
	case *upstreams.LabelsWrapper:
		return LabelsToApi(w.Labels)
	case *upstreams.SubMethodsWrapper:
		return SubMethodsToApi(w.SubMethods)
	}
	return nil
}

func toFullResponse(grpcId int, nodeGroupId string, state upstreams.ChainSupervisorState) *dshackle.SubscribeChainStatusResponse {
	return &dshackle.SubscribeChainStatusResponse{
		ChainDescription: &dshackle.ChainDescription{
			Chain: dshackle.ChainRef(grpcId),
			ChainEvent: []*dshackle.ChainEvent{
				ChainStatusToApi(state.Status),
				SupportedMethodsToApi(state.Methods.GetSupportedMethods().ToSlice()),
				LowerBoundsToApi(lo.Values(state.LowerBounds)),
				HeadToApi(state.HeadData.Head),
				BlocksToApi(state.Blocks),
				SubMethodsToApi(state.SubMethods.ToSlice()),
				LabelsToApi(state.ChainLabels),
			},
			NodeGroupId: nodeGroupId,
		},
		BuildInfo: &dshackle.BuildInfo{
			Version: buildinfo.ProductVersion(),
		},
		FullResponse: true,
	}
}
