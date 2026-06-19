package flow

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/rs/zerolog/log"
)

const (
	// pendingTxBufferSize is the per-subscriber fan-out buffer for the pending-tx
	// sources. Pending hashes arrive in bursts (a busy mempool yields many per
	// second), so it is far larger than the engine default; a client that cannot
	// keep up is disconnected as too slow rather than dropping events silently.
	pendingTxBufferSize = 4096
	// pendingTxDedupSize bounds the dedup cache; pendingTxDedupTTL is how long a
	// hash is remembered. Mirrors dshackle's AggregatedPendingTxes (10k / 30s):
	// pending txs are ephemeral, so a short TTL is enough to drop the duplicates
	// the same tx produces across several upstreams' mempools.
	pendingTxDedupSize = 10_000
	pendingTxDedupTTL  = 30 * time.Second
	// pendingEnrichConcurrency bounds how many eth_getTransactionByHash enrichments
	// run at once for drpc_pendingTransactions. Enrichment is far slower than the
	// rate hashes arrive (a network round-trip each, and the common dropped/mined-out
	// hash waits on every upstream replying null), so a serial drain cannot keep up
	// with a busy mempool
	pendingEnrichConcurrency = 32
)

// upstreamSub bundles a per-upstream pending subscription with the connector that
// owns it, so the source can tear every feed down on Stop.
type upstreamSub struct {
	conn connectors.ApiConnector
	opId string
}

// newPendingTxSourceBuilder builds the chain's single shared
// newPendingTransactions source. Unlike the generic node-backed source (one
// upstream), it opens eth_subscribe("newPendingTransactions") on EVERY ws-capable
// upstream and merges them, because different nodes hold different mempools, then
// dedupes by hash so a tx seen on several upstreams is emitted once. It mirrors
// dshackle's AggregatedPendingTxes.
//
// A single dead/erroring upstream only kills its own feed - the merged source stays
// alive as long as at least one feed is still feeding it. It terminates (emits a
// terminal frame so clients fail over to the generic node-backed path) when the
// chain loses PendingTxCap, OR when every feed has died (the feeds are fixed at
// build time and never reconnect, so a source with no live feeds is permanently
// silent - PendingTxCap may still be present on a different upstream that this
// source did not subscribe to). Mirrors the terminate-on-cap-loss contract of the
// local newHeads/logs sources. The source emits tx hashes; drpc_pendingTransactions
// rides this same source and enriches them (see newDrpcPendingTxSourceBuilder).
//
// Limitation: the merge set is fixed at source-creation time. Upstreams added
// later are not picked up until the source is torn down and rebuilt (matches
// dshackle building from getAll() at subscription time).
func newPendingTxSourceBuilder(
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
) subengine.SourceBuilder {
	return func(srcCtx context.Context) (*subengine.Source, error) {
		chainSup := supervisor.GetChainSupervisor(chain)
		if chainSup == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		request, err := protocol.NewInternalSubUpstreamJsonRpcRequest("eth_subscribe", []any{"newPendingTransactions"}, chain)
		if err != nil {
			return nil, err
		}

		merged := make(chan *protocol.WsResponse, pendingTxBufferSize)
		// feedDone receives one signal per feed that dies on its own (not via srcCtx);
		// when all feeds are gone the source terminates. Unbuffered: the forwarder
		// races the send against srcCtx so it never leaks after the source returns.
		feedDone := make(chan struct{})
		subs := make([]upstreamSub, 0)

		for _, id := range chainSup.GetUpstreamIds() {
			state := chainSup.GetUpstreamState(id)
			if state == nil || state.Status != protocol.Available {
				continue
			}
			if state.Caps == nil || !state.Caps.Contains(protocol.PendingTxCap) {
				continue
			}
			upstream := supervisor.GetUpstream(id)
			if upstream == nil {
				continue
			}
			wsConn := getMethodConnector(upstream, request.SpecMethod())
			if wsConn == nil {
				continue
			}
			subResp, err := wsConn.Subscribe(srcCtx, request)
			if err != nil {
				log.Warn().Err(err).Msgf("subengine: failed to open newPendingTransactions sub on %s", id)
				continue
			}
			subs = append(subs, upstreamSub{conn: wsConn, opId: subResp.OpId()})
			go forwardPendingFeed(srcCtx, id, subResp.ResponseChan(), merged, feedDone)
		}

		if len(subs) == 0 {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		out := make(chan *protocol.WsResponse, pendingTxBufferSize)
		// Watch the chain caps so the source terminates when PendingTxCap is lost
		// (the last ws upstream left). It then emits a terminal frame and clients
		// fail over to the generic node-backed path - same contract as the local
		// newHeads/logs sources.
		stateSub := chainSup.SubscribeState(fmt.Sprintf("subengine_pendingtx_%s_%s", chain, uuid.NewString()))
		go func() {
			defer close(out)
			defer stateSub.Unsubscribe()
			seen := lru.NewLRU[string, struct{}](pendingTxDedupSize, nil, pendingTxDedupTTL)
			feedsAlive := len(subs)

			pendingTxLost := func() bool {
				caps := chainSup.GetChainState().Caps
				return caps == nil || !caps.Contains(protocol.PendingTxCap)
			}
			if pendingTxLost() {
				out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
				return
			}

			for {
				select {
				case <-srcCtx.Done():
					return
				case _, ok := <-stateSub.Events:
					if !ok {
						return
					}
					if pendingTxLost() {
						out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
						return
					}
				case <-feedDone:
					// A feed died on its own; once they are all gone the source can
					// never produce again, so terminate and let clients fail over.
					feedsAlive--
					if feedsAlive == 0 {
						out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
						return
					}
				case r, ok := <-merged:
					if !ok {
						return
					}
					key := string(r.Message)
					if seen.Contains(key) {
						continue // same tx hash already emitted from another upstream
					}
					seen.Add(key, struct{}{})
					select {
					case out <- &protocol.WsResponse{Message: r.Message, UpstreamId: r.UpstreamId}:
					case <-srcCtx.Done():
						return
					}
				}
			}
		}()

		stop := func() {
			for _, s := range subs {
				s.conn.Unsubscribe(s.opId)
			}
		}
		return &subengine.Source{Events: out, Stop: stop, Buffer: pendingTxBufferSize}, nil
	}
}

// forwardPendingFeed drains one upstream's pending subscription into the merged
// channel until srcCtx is cancelled or the feed ends. The upstream's own
// subscription-confirmation frame (SubId == "") is swallowed. An error frame or
// closed channel ends only this feed - it is NOT terminal for the merged source,
// which keeps serving from the surviving upstreams - and signals feedDone so the
// source can terminate once every feed has died. A feed that exits because srcCtx
// was cancelled does NOT signal (the source is already tearing down).
func forwardPendingFeed(
	srcCtx context.Context,
	upstreamId string,
	in chan *protocol.WsResponse,
	merged chan<- *protocol.WsResponse,
	feedDone chan<- struct{},
) {
	for {
		select {
		case <-srcCtx.Done():
			return
		case r, ok := <-in:
			if !ok || r.Error != nil {
				select {
				case feedDone <- struct{}{}:
				case <-srcCtx.Done():
				}
				return
			}
			if r.SubId == "" {
				continue // upstream's own subscription confirmation
			}
			r.UpstreamId = upstreamId
			select {
			case merged <- r:
			case <-srcCtx.Done():
				return
			}
		}
	}
}

// newDrpcPendingTxSourceBuilder builds the drpc_pendingTransactions source. It
// rides the SAME shared newPendingTransactions hash source via the engine (so both
// topics share one set of node subscriptions) and enriches each hash into a full
// transaction object via eth_getTransactionByHash on a best-rated upstream. Hashes
// whose tx is no longer retrievable (mined-out / dropped -> null result) are
// skipped; this is normal for the mempool and not a failure.
func newDrpcPendingTxSourceBuilder(
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	engine subengine.Engine,
) subengine.SourceBuilder {
	return func(srcCtx context.Context) (*subengine.Source, error) {
		chainSup := supervisor.GetChainSupervisor(chain)
		if chainSup == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		// Attach to the shared hash source. This drives a DIFFERENT actor than the
		// drpc key, so the engine building this source while we subscribe to the
		// pending key cannot deadlock.
		inner, err := engine.Subscribe(localPendingTxKey, newPendingTxSourceBuilder(supervisor, chain))
		if err != nil {
			return nil, err
		}

		out := make(chan *protocol.WsResponse, pendingTxBufferSize)
		go func() {
			defer close(out)
			// counting semaphore pattern
			sem := make(chan struct{}, pendingEnrichConcurrency)
			var wg sync.WaitGroup

			emit := func(resp *protocol.WsResponse) {
				select {
				case out <- resp:
				case <-srcCtx.Done():
				}
			}

			for {
				select {
				case <-srcCtx.Done():
					wg.Wait() // workers exit via srcCtx; wait so none send on a closed out
					return
				case ev, ok := <-inner.Events:
					if !ok {
						// The shared hash source ended; wait for in-flight enrichments to
						// drain (so they don't send on a closed out), then propagate its
						// terminal cause so drpc clients fail over rather than stalling.
						wg.Wait()
						if cause := inner.Err(); cause != nil {
							emit(&protocol.WsResponse{Error: cause})
						}
						return
					}
					// Acquire a slot before spawning so concurrency stays bounded;
					// bail out promptly if the source is torn down while we wait.
					select {
					case sem <- struct{}{}:
					case <-srcCtx.Done():
						wg.Wait()
						return
					}
					wg.Add(1)
					go func(hashMessage []byte) {
						defer wg.Done()
						defer func() { <-sem }()
						tx, upstreamId := enrichPendingTx(srcCtx, supervisor, chain, chainSup, hashMessage)
						if tx == nil {
							return // not found / errored - normal for a dropped pending tx
						}
						emit(&protocol.WsResponse{Message: tx, UpstreamId: upstreamId})
					}(ev.Message)
				}
			}
		}()

		stop := func() { inner.Unsubscribe() }
		return &subengine.Source{Events: out, Stop: stop, Buffer: pendingTxBufferSize}, nil
	}
}

// enrichPendingTx resolves a pending tx hash (a JSON string like "0x..") into its
// full transaction object via eth_getTransactionByHash. A pending hash lives in
// SOME node's mempool, so it BROADCASTS the request to every available upstream
// (no rating/matchers) and returns the first non-null result - a single
// rating-picked upstream usually does not have the tx and would drop most of them.
// This mirrors dshackle's drpc_pendingTransactions
// (Flux.fromIterable(getUpstreams()).flatMap(read).next()).
//
// It returns (nil, "") when the hash cannot be parsed or no upstream returns the tx
// (every reply null/error) - all non-terminal, the tx simply left the mempool.
func enrichPendingTx(
	ctx context.Context,
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	chainSup upstreams.ChainSupervisor,
	hashMessage []byte,
) ([]byte, string) {
	var hash string
	if err := sonic.Unmarshal(hashMessage, &hash); err != nil {
		return nil, ""
	}

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getTransactionByHash", []any{hash}, chain)
	if err != nil {
		return nil, ""
	}

	ids := chainSup.GetUpstreamIds()
	// First non-null wins; the deferred cancel stops the remaining in-flight calls.
	bcastCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type txResult struct {
		tx         []byte
		upstreamId string
	}
	results := make(chan txResult, len(ids))
	parsedParam := request.ParseParams(bcastCtx)

	sent := 0
	for _, id := range ids {
		state := chainSup.GetUpstreamState(id)
		if state == nil || state.Status != protocol.Available {
			continue
		}
		upstream := supervisor.GetUpstream(id)
		if upstream == nil || getMethodConnector(upstream, request.SpecMethod()) == nil {
			continue
		}
		sent++
		go func(up upstreams.Upstream) {
			resp, err := sendUnaryRequest(bcastCtx, up, request, parsedParam)
			if err != nil || resp.Response.HasError() {
				results <- txResult{}
				return
			}
			result := resp.Response.ResponseResult()
			if isNullResult(result) {
				results <- txResult{}
				return
			}
			// Copy: connector buffers may be pooled and reused after this returns.
			results <- txResult{tx: append([]byte(nil), result...), upstreamId: resp.UpstreamId}
		}(upstream)
	}

	for i := 0; i < sent; i++ {
		select {
		case <-ctx.Done():
			return nil, ""
		case r := <-results:
			if r.tx != nil {
				return r.tx, r.upstreamId
			}
		}
	}
	return nil, ""
}

// isNullResult reports whether a json-rpc result is the JSON null literal (an
// unknown/dropped tx hash yields {"result": null}).
func isNullResult(result []byte) bool {
	return len(result) == 0 || bytes.Equal(bytes.TrimSpace(result), []byte("null"))
}
