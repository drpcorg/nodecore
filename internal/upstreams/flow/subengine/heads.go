package subengine

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/google/uuid"
)

// NewHeadsSourceBuilder builds a locally-synthesized newHeads source: instead of
// opening eth_subscribe("newHeads") on a node, it taps the chain's merged head
// stream (fork-choice winner) and emits each new block's full header JSON.
//
// Only blocks carrying RawData are emitted - that JSON is populated solely by a
// subscription-driven head (EVM ParseSubscriptionBlock). Polled heads have no
// RawData and are skipped; callers gate this builder on WS-head availability so
// that does not happen in practice.
func NewHeadsSourceBuilder(sup upstreams.UpstreamSupervisor, chain chains.Chain) SourceBuilder {
	return func(srcCtx context.Context) (*Source, error) {
		chainSup := sup.GetChainSupervisor(chain)
		if chainSup == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		sub := chainSup.SubscribeState(fmt.Sprintf("subengine_newheads_%s_%s_%d", chain, uuid.NewString(), time.Now().UnixNano()))
		out := make(chan *protocol.WsResponse, 100)

		go func() {
			defer close(out)
			defer sub.Unsubscribe()

			// Seed with the current head so the first subscriber gets an
			// immediate event instead of waiting for the next block.
			if cur := chainSup.GetChainState().HeadData; !cur.IsEmpty() && len(cur.Head.RawData) > 0 {
				out <- &protocol.WsResponse{Message: cur.Head.RawData, UpstreamId: cur.UpstreamId}
			}

			for {
				select {
				case <-srcCtx.Done():
					return
				case event, ok := <-sub.Events:
					if !ok {
						return
					}
					for _, wrapper := range event.Wrappers {
						head, ok := wrapper.(*upstreams.HeadWrapper)
						if !ok || len(head.Head.RawData) == 0 {
							continue
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
