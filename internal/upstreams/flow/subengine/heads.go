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
// opening eth_subscribe("newHeads") on a node, it follows a filtered head feed
// (ChainSupervisor.SubscribeHead) over the upstreams the caller selected - the
// available ones with a subscription-driven head, narrowed by the client's
// selectors - and forwards each head's notification payload.
//
// A head produced from a ws newHeads notification carries that notification's
// header JSON in RawData (set only by ParseSubscriptionBlock) - which is exactly
// the newHeads payload, so it is forwarded verbatim. The filter admits only
// subscription-driven heads, so a head without RawData is not expected; it is
// skipped defensively. The feed's initial head is forwarded like any other, so
// the source's first client receives the current head.
//
// The source terminates with a terminal frame - clients resubscribe and are
// resolved afresh - when the feed reports HeadFeedEmpty: no upstream passes the
// filter any more.
func NewHeadsSourceBuilder(sup upstreams.UpstreamSupervisor, chain chains.Chain, filter upstreams.FilterUpstream) SourceBuilder {
	return func(srcCtx context.Context) (*Source, error) {
		chainSup := sup.GetChainSupervisor(chain)
		if chainSup == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		feed := chainSup.SubscribeHead(fmt.Sprintf("subengine_newheads_%s_%s", chain, uuid.NewString()), filter)
		out := make(chan protocol.SubResponse, 100)

		go func() {
			defer close(out)
			defer feed.Unsubscribe()

			for {
				select {
				case <-srcCtx.Done():
					return
				case event, ok := <-feed.Events:
					if !ok {
						return
					}
					switch e := event.(type) {
					case upstreams.HeadFeedEmpty:
						out <- &protocol.GenericSubResponse{Error: protocol.SubscribeTotalFailureError()}
						return
					case upstreams.HeadUpdated:
						if len(e.Head.RawData) == 0 {
							continue // not a subscription block - nothing to forward
						}
						out <- &protocol.GenericSubResponse{Message: e.Head.RawData, UpstreamId: e.UpstreamId}
					}
				}
			}
		}()

		// Teardown is driven by srcCtx cancellation (the goroutine unsubscribes
		// from the feed and closes out on return).
		return &Source{Events: out, Stop: func() {}}, nil
	}
}
