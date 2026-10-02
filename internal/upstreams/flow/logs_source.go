package flow

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// logsBlocksSkippedMetric counts blocks whose logs could not be served before the
// give-up deadline; the source then terminates, so subscribers get an error
// instead of a gap. The reason label says why.
var logsBlocksSkippedMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "logs_source",
		Name:      "blocks_skipped_total",
		Help:      "The total number of blocks whose logs could not be served in time (the source terminates), by reason",
	},
	[]string{"chain", "reason"},
)

// logsUpstreamWaitMetric is how long a request of the logs source (eth_getLogs of
// a block, eth_getBlockByHash of a backfilled ancestor) waited for an upstream
// able to serve it, observed only when it had to wait.
var logsUpstreamWaitMetric = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: config.AppName,
		Subsystem: "logs_source",
		Name:      "upstream_wait_seconds",
		Help:      "How long a logs source request waited for an upstream able to serve it, by method",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
	},
	[]string{"chain", "method"},
)

func init() {
	prometheus.MustRegister(logsBlocksSkippedMetric, logsUpstreamWaitMetric)
}

const (
	// logsCacheSize is how many recent blocks' logs are kept so a reorg DROP can
	// re-emit them with removed:true.
	logsCacheSize = 32
	// logsBufferSize is the per-subscriber fan-out buffer for the logs source: a
	// single busy block can yield thousands of logs, so it is far larger than the
	// engine default. A client that cannot drain a block's worth of logs in time
	// is disconnected as too slow (no silent gaps).
	logsBufferSize = 4096
	// When no upstream can serve a block yet, it re-selects with a backoff from
	// logsUpstreamWaitStep up to logsUpstreamWaitMax.
	logsUpstreamWaitStep = 50 * time.Millisecond
	logsUpstreamWaitMax  = time.Second
	// A block waits logsGiveUpBlocks block times, clamped to [min, max], before the
	// source terminates.
	logsGiveUpBlocks = 10
	logsGiveUpMin    = 3 * time.Second
	logsGiveUpMax    = time.Minute
)

// newLogsSourceBuilder builds the chain's single shared "all logs" source: for
// each new block it issues one eth_getLogs{blockHash} (no address/topic filter)
// and emits every log as its own event; per-client address/topic filtering
// happens in the processor. Upstream selection is by the block's HEIGHT (any
// available upstream at >= that height), not by the head producer, so a producer
// that has since gone away does not break log delivery.
//
// No block is skipped silently. A block waits for an upstream able to serve it:
// the merged head may come from an upstream without eth_getLogs while the ones
// with it are a moment behind. Heights the merged head jumped over, and the new
// chain after a reorg, are backfilled by the block-update stream. A block that
// cannot be served in time terminates the source, so subscribers get an error
// instead of a gap.
//
// Reorgs are handled via the block-update stream (see subengine.StreamBlockUpdates):
// a dropped block's cached logs are re-emitted with removed:true. The source
// terminates (so clients fail over to the generic node-backed path) when the
// chain loses LogsCap.
func newLogsSourceBuilder(
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	registry *rating.RatingRegistry,
) subengine.SourceBuilder {
	return func(srcCtx context.Context) (*subengine.Source, error) {
		chainSup := supervisor.GetChainSupervisor(chain)
		if chainSup == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		logsLost := func() bool {
			caps := chainSup.GetChainState().Caps
			return caps == nil || !caps.Contains(protocol.LogsCap)
		}
		giveUp := logsGiveUp(chain)

		out := make(chan protocol.SubResponse, logsBufferSize)
		updates := make(chan subengine.BlockUpdate, 64)

		go subengine.StreamBlockUpdates(srcCtx, chainSup, updates, blockByHashResolver(supervisor, chain, chainSup, registry, giveUp))

		go func() {
			defer close(out)
			cache := newLogCache(logsCacheSize)

			fail := func() {
				select {
				case out <- &protocol.GenericSubResponse{Error: protocol.SubscribeTotalFailureError()}:
				case <-srcCtx.Done():
				}
			}

			if logsLost() {
				fail()
				return
			}

			for {
				select {
				case <-srcCtx.Done():
					return
				case update, ok := <-updates:
					if !ok {
						return // the stream gave up on a backfill: the engine reports a total failure
					}
					if logsLost() {
						fail()
						return
					}
					switch update.Kind {
					case subengine.BlockNew:
						logs, upstreamId, err := fetchBlockLogs(srcCtx, supervisor, chain, chainSup, registry, update.Block, giveUp)
						if err != nil {
							fail()
							return
						}
						// Parse each log's filterable fields once here; every client's
						// SubFilter then reads the shared parsed view instead of
						// re-parsing the raw JSON per subscriber.
						parsed := make([]*parsedLog, len(logs))
						for i, raw := range logs {
							parsed[i] = parseLogEvent(raw)
						}
						cache.put(update.Block.Hash.ToHex(), parsed)
						for _, pl := range parsed {
							select {
							case out <- &protocol.GenericSubResponse{Message: pl.raw, UpstreamId: upstreamId, ParsedEvent: pl}:
							case <-srcCtx.Done():
								return
							}
						}
					case subengine.BlockDrop:
						// Client contract: this source is shared and cache-only
						// (logsCacheSize blocks), so a client that subscribed after a
						// block was emitted but before it reorgs receives removed:true
						// for logs it never received as added. Clients MUST tolerate
						// unmatched/spurious removed events (standard eth-log semantics).
						cached, ok := cache.get(update.Block.Hash.ToHex())
						if !ok {
							continue // never cached this block's logs - nothing to revert
						}
						// Reuse the cached parsed view: the removed flag does not affect
						// address/topic matching, so per-client filters still apply.
						for _, pl := range cached {
							select {
							case out <- &protocol.GenericSubResponse{Message: setRemovedTrue(pl.raw), ParsedEvent: pl}:
							case <-srcCtx.Done():
								return
							}
						}
					}
				}
			}
		}()

		// Teardown is driven by srcCtx cancellation: both goroutines unwind and
		// out is closed by the consumer goroutine.
		return &subengine.Source{Events: out, Stop: func() {}, Buffer: logsBufferSize}, nil
	}
}

// logsGiveUp is how long one block may wait for an upstream able to serve it.
func logsGiveUp(chain chains.Chain) time.Duration {
	giveUp := logsGiveUpBlocks * chains.GetChain(chain.String()).Settings.ExpectedBlockTime
	return min(max(giveUp, logsGiveUpMin), logsGiveUpMax)
}

// fetchBlockLogs returns the raw log objects of block, fetched via eth_getLogs on
// an upstream chosen by height, plus the serving upstream id. It errors only when
// no upstream served the block within giveUp.
func fetchBlockLogs(
	ctx context.Context,
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	chainSup upstreams.ChainSupervisor,
	registry *rating.RatingRegistry,
	block protocol.Block,
	giveUp time.Duration,
) ([]json.RawMessage, string, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_getLogs",
		[]any{map[string]string{"blockHash": block.Hash.ToHexWithPrefix()}},
		chain,
	)
	if err != nil {
		log.Warn().Err(err).Msgf("subengine: failed to build eth_getLogs for block %d on %s", block.Height, chain)
		logsBlocksSkippedMetric.WithLabelValues(chain.String(), "build").Inc()
		return nil, "", err
	}

	// A fresh strategy carries the height matcher; repeated SelectUpstream calls
	// walk down the rating list (selectedUpstreams dedup).
	newStrategy := func() UpstreamStrategy {
		return NewRatingStrategy(chain, "eth_getLogs", []Matcher{NewHeightMatcher(int64(block.Height))}, chainSup, registry)
	}
	var logs []json.RawMessage
	parse := func(result []byte) bool {
		var arr []json.RawMessage
		if err := sonic.Unmarshal(result, &arr); err != nil {
			log.Warn().Err(err).Msgf("subengine: failed to parse eth_getLogs result for block %d on %s", block.Height, chain)
			return false
		}
		logs = make([]json.RawMessage, 0, len(arr))
		for _, l := range arr {
			logs = append(logs, append(json.RawMessage(nil), l...)) // copy: connector buffers may be pooled
		}
		return true
	}

	resp, reason, err := sendUntil(ctx, supervisor, chain, request, newStrategy, giveUp, parse)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn().Err(err).Msgf("subengine: no upstream served eth_getLogs for block %d on %s within %s; terminating the logs source", block.Height, chain, giveUp)
			logsBlocksSkippedMetric.WithLabelValues(chain.String(), reason).Inc()
		}
		return nil, "", err
	}
	return logs, resp.UpstreamId, nil
}

// blockByHashResolver lets the block-update stream fetch the merged head's
// ancestors by hash: heights the head jumped over, or the new chain after a reorg.
func blockByHashResolver(
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	chainSup upstreams.ChainSupervisor,
	registry *rating.RatingRegistry,
	giveUp time.Duration,
) subengine.BlockResolver {
	return func(ctx context.Context, hash blockchain.HashId) (protocol.Block, error) {
		request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBlockByHash", []any{hash.ToHexWithPrefix(), false}, chain)
		if err != nil {
			return protocol.Block{}, err
		}
		newStrategy := func() UpstreamStrategy {
			return NewRatingStrategy(chain, "eth_getBlockByHash", nil, chainSup, registry)
		}
		var block protocol.Block
		parse := func(result []byte) bool {
			var header struct {
				Hash   string           `json:"hash"`
				Parent string           `json:"parentHash"`
				Number *rpc.BlockNumber `json:"number"`
			}
			// null (an upstream that does not know the block yet) has no number
			if err := sonic.Unmarshal(result, &header); err != nil || header.Number == nil {
				return false
			}
			block = protocol.Block{
				Height:     uint64(header.Number.Int64()),
				Hash:       blockchain.NewHashIdFromString(header.Hash),
				ParentHash: blockchain.NewHashIdFromString(header.Parent),
			}
			return block.Hash.Equals(hash)
		}
		if _, _, err := sendUntil(ctx, supervisor, chain, request, newStrategy, giveUp, parse); err != nil {
			return protocol.Block{}, err
		}
		return block, nil
	}
}

// sendUntil sends request to a strategy-chosen upstream until accept takes its
// result or giveUp passes. An upstream error or a rejected result moves on to the
// next-best upstream; when none is selectable, it backs off and re-selects from
// the full list. On failure reason names the last cause, for the skip metric.
func sendUntil(
	ctx context.Context,
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	request protocol.RequestHolder,
	newStrategy func() UpstreamStrategy,
	giveUp time.Duration,
	accept func(result []byte) bool,
) (*protocol.ResponseHolderWrapper, string, error) {
	start := time.Now()
	wait := time.Duration(0)
	strategy, fresh := newStrategy(), true
	reason := "no_upstream"
	for {
		resp, err := selectAndSend(ctx, supervisor, request, strategy)
		if err == nil {
			if !resp.Response.HasError() && accept(resp.Response.ResponseResult()) {
				if wait > 0 {
					logsUpstreamWaitMetric.WithLabelValues(chain.String(), request.Method()).Observe(time.Since(start).Seconds())
				}
				return resp, "", nil
			}
			reason, fresh = "upstream_error", false
			continue
		}
		if fresh {
			reason = "no_upstream"
		}
		if time.Since(start) >= giveUp {
			return nil, reason, err
		}
		wait = min(max(2*wait, logsUpstreamWaitStep), logsUpstreamWaitMax)
		select {
		case <-ctx.Done():
			return nil, reason, ctx.Err()
		case <-time.After(wait):
		}
		strategy, fresh = newStrategy(), true
	}
}

// setRemovedTrue returns a copy of an eth log object with "removed" set to true,
// for re-emitting a reorged-out block's logs. On any parse/marshal error it
// returns the input unchanged and warns: a malformed cached log would otherwise
// be re-emitted with its original "removed" value, so the client would treat a
// reorged-out log as still valid without any signal.
func setRemovedTrue(raw json.RawMessage) []byte {
	node, err := sonic.Get(raw)
	if err != nil {
		log.Warn().Err(err).Msg("subengine: failed to parse cached log for reorg removal; re-emitting unchanged")
		return raw
	}
	if _, err := node.Set("removed", ast.NewBool(true)); err != nil {
		log.Warn().Err(err).Msg("subengine: failed to set removed:true on cached log; re-emitting unchanged")
		return raw
	}
	b, err := node.MarshalJSON()
	if err != nil {
		log.Warn().Err(err).Msg("subengine: failed to marshal cached log for reorg removal; re-emitting unchanged")
		return raw
	}
	return b
}

// logCache is a single-goroutine FIFO of recent blocks' parsed logs, keyed by
// block hash, used to re-emit removals on a reorg. It stores the parsed view
// (which also carries the raw bytes) so a DROP reuses it without re-parsing.
type logCache struct {
	capacity int
	order    []string
	items    map[string][]*parsedLog
}

func newLogCache(capacity int) *logCache {
	return &logCache{capacity: capacity, items: make(map[string][]*parsedLog, capacity)}
}

func (c *logCache) put(hash string, logs []*parsedLog) {
	if _, ok := c.items[hash]; ok {
		c.items[hash] = logs
		return
	}
	if len(c.order) >= c.capacity {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}
	c.order = append(c.order, hash)
	c.items[hash] = logs
}

func (c *logCache) get(hash string) ([]*parsedLog, bool) {
	logs, ok := c.items[hash]
	return logs, ok
}
