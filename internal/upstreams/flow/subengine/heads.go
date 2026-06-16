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
// opening eth_subscribe("newHeads") on a node, it taps the chain's merged head
// stream (fork-choice winner) and forwards subscription blocks.
//
// A head produced from a ws newHeads notification carries that notification's
// header JSON in RawData (set only by ParseSubscriptionBlock) - which is exactly
// the newHeads payload, so it is forwarded verbatim. Heads without RawData
// (polled blocks, or a subscription head's poll fallback) are not subscription
// notifications and are skipped.
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

		sub := chainSup.SubscribeState(fmt.Sprintf("subengine_newheads_%s_%s", chain, uuid.NewString()))
		out := make(chan *protocol.WsResponse, 100)

		go func() {
			defer close(out)
			defer sub.Unsubscribe()

			newHeadsLost := func() bool {
				caps := chainSup.GetChainState().Caps
				return caps == nil || !caps.Contains(protocol.NewHeadsCap)
			}

			if newHeadsLost() {
				out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
				return
			}

			for {
				select {
				case <-srcCtx.Done():
					return
				case event, ok := <-sub.Events:
					if !ok {
						return
					}
					if newHeadsLost() {
						out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
						return
					}
					for _, wrapper := range event.Wrappers {
						head, ok := wrapper.(*upstreams.HeadWrapper)
						if !ok || len(head.Head.RawData) == 0 {
							continue // not a subscription block - nothing to forward
						}
						out <- &protocol.WsResponse{Message: head.Head.RawData, UpstreamId: head.UpstreamId}
					}
				}
			}
		}()

		// Teardown is driven by srcCtx cancellation (the goroutine unsubscribes
		// from the chain state and closes out on return).
		return &Source{Events: out, Stop: func() {}}, nil
	}
}
