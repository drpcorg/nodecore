package emerald

import (
	"context"
	"errors"
	"fmt"

	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

var errNilUpstreamSupervisor = errors.New("upstream supervisor cannot be nil")

func SubscribeChainStatus(
	upstreamSupervisor upstreams.UpstreamSupervisor,
	stream dshackle.Blockchain_SubscribeChainStatusServer,
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
		subscribeChainSupervisorStates(ctx, chainSupervisor, chainSubs, responses)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case chainSupervisorEvent, ok := <-chainSupervisorEventsSub.Events:
			if ok {
				switch c := chainSupervisorEvent.(type) {
				case *upstreams.AddChainSupervisorEvent:
					subscribeChainSupervisorStates(ctx, c.ChainSupervisor, chainSubs, responses)
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
			if !sendResponse(ctx, responses, toFullResponse(grpcId, state)) {
				return
			}
			fullSent = true
		}

		for {
			select {
			case <-ctx.Done():
				return
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
						continue
					}
					if !sendResponse(ctx, responses, stateWrappersToResponse(grpcId, event.Wrappers)) {
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
	select {
	case <-ctx.Done():
		return false
	case responses <- resp:
		return true
	}
}

func stateWrappersToResponse(grpcId int, wrappers []upstreams.ChainSupervisorStateWrapper) *dshackle.SubscribeChainStatusResponse {
	events := make([]*dshackle.ChainEvent, len(wrappers))

	for i, wrapper := range wrappers {
		switch w := wrapper.(type) {
		case *upstreams.HeadWrapper:
			events[i] = HeadToApi(w.Head)
		case *upstreams.BlocksWrapper:
			events[i] = BlocksToApi(w.Blocks)
		case *upstreams.MethodsWrapper:
			events[i] = SupportedMethodsToApi(w.Methods)
		case *upstreams.StatusWrapper:
			events[i] = ChainStatusToApi(w.Status)
		case *upstreams.LowerBoundsWrapper:
			events[i] = LowerBoundsToApi(w.LowerBounds)
		case *upstreams.LabelsWrapper:
			events[i] = LabelsToApi(w.Labels)
		case *upstreams.SubMethodsWrapper:
			events[i] = SubMethodsToApi(w.SubMethods)
		}
	}

	return &dshackle.SubscribeChainStatusResponse{
		ChainDescription: &dshackle.ChainDescription{
			Chain:      dshackle.ChainRef(grpcId),
			ChainEvent: events,
		},
	}
}

func toFullResponse(grpcId int, state upstreams.ChainSupervisorState) *dshackle.SubscribeChainStatusResponse {
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
		},
		//TODO: hardcoded value, then it should be fixed
		BuildInfo: &dshackle.BuildInfo{
			Version: "1.0.0",
		},
		FullResponse: true,
	}
}
