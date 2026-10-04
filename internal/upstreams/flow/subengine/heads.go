package subengine

import (
	"context"
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/google/uuid"
)

// NewHeadsSourceBuilder builds a locally-synthesized newHeads source: instead of
// opening eth_subscribe("newHeads") on a node, it forwards the chain's newHeads feed
// (ChainSupervisor.SubscribeNewHeads) - the ws newHeads payloads of upstreams, in order,
// including heights the fork choice took from a polled head.
//
// The source degrades - emits a terminal frame so clients resubscribe onto the
// generic node-backed path - when the chain loses NewHeadsCap (the last ws-head
// upstream left), detected by re-reading the chain state on each state event.
func NewHeadsSourceBuilder(sup upstreams.UpstreamSupervisor, chain chains.Chain) SourceBuilder {
	return func(srcCtx context.Context) (*Source, error) {
		chainSup := sup.GetChainSupervisor(chain)
		if chainSup == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		subId := uuid.NewString()
		sub := chainSup.SubscribeState(fmt.Sprintf("subengine_newheads_%s_%s", chain, subId))
		heads := chainSup.SubscribeNewHeads(fmt.Sprintf("subengine_newheads_%s_%s", chain, subId))
		out := make(chan protocol.SubResponse, 100)

		go func() {
			defer close(out)
			defer sub.Unsubscribe()
			defer heads.Unsubscribe()

			newHeadsLost := func() bool {
				caps := chainSup.GetChainState().Caps
				return caps == nil || !caps.Contains(protocol.NewHeadsCap)
			}

			if newHeadsLost() {
				out <- &protocol.GenericSubResponse{Error: protocol.SubscribeTotalFailureError()}
				return
			}

			for {
				select {
				case <-srcCtx.Done():
					return
				case _, ok := <-sub.Events:
					if !ok {
						return
					}
					if newHeadsLost() {
						out <- &protocol.GenericSubResponse{Error: protocol.SubscribeTotalFailureError()}
						return
					}
				case head, ok := <-heads.Events:
					if !ok {
						return
					}
					if len(head.Head.RawData) == 0 {
						continue
					}
					out <- &protocol.GenericSubResponse{Message: head.Head.RawData, UpstreamId: head.UpstreamId}
				}
			}
		}()

		// Teardown is driven by srcCtx cancellation (the goroutine unsubscribes
		// from the chain state and closes out on return).
		return &Source{Events: out, Stop: func() {}}, nil
	}
}
