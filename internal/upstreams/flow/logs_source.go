package flow

import (
	"context"
	"encoding/json"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// logsBlocksSkippedMetric counts blocks whose logs could not be served and were
// therefore skipped (the client silently misses that block's logs). A non-zero
// rate means subscribers may have gaps; the reason label says why.
var logsBlocksSkippedMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "logs_source",
		Name:      "blocks_skipped_total",
		Help:      "The total number of blocks whose logs could not be served and were skipped, by reason",
	},
	[]string{"chain", "reason"},
)

func init() {
	prometheus.MustRegister(logsBlocksSkippedMetric)
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
	// logsFetchAttempts bounds the per-block walk down the rating list when an
	// upstream errors on eth_getLogs before the block is skipped.
	logsFetchAttempts = 3
)

// newLogsSourceBuilder builds the chain's single shared "all logs" source: for
// each new block it issues one eth_getLogs{blockHash} (no address/topic filter)
// and emits every log as its own event; per-client address/topic filtering
// happens in the processor. Upstream selection is by the block's HEIGHT (any
// available upstream at >= that height), not by the head producer, so a producer
// that has since gone away does not break log delivery.
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

		out := make(chan *protocol.WsResponse, logsBufferSize)
		updates := make(chan subengine.BlockUpdate, 64)

		go subengine.StreamBlockUpdates(srcCtx, chainSup, updates)

		go func() {
			defer close(out)
			cache := newLogCache(logsCacheSize)

			if logsLost() {
				out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
				return
			}

			for {
				select {
				case <-srcCtx.Done():
					return
				case update, ok := <-updates:
					if !ok {
						return
					}
					if logsLost() {
						out <- &protocol.WsResponse{Error: protocol.WsTotalFailureError()}
						return
					}
					switch update.Kind {
					case subengine.BlockNew:
						logs, upstreamId := fetchBlockLogs(srcCtx, supervisor, chain, chainSup, registry, update.Block)
						if logs == nil {
							continue // fetch failed/skipped (logged); not terminal
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
							case out <- &protocol.WsResponse{Message: pl.raw, UpstreamId: upstreamId, ParsedEvent: pl}:
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
							case out <- &protocol.WsResponse{Message: setRemovedTrue(pl.raw), ParsedEvent: pl}:
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

// fetchBlockLogs returns the raw log objects of block, fetched via eth_getLogs on
// an upstream chosen by height, plus the serving upstream id. It returns (nil,"")
// when the block cannot be served (no upstream at the height, or every attempt
// errored); the source treats that as a skipped block, not a terminal failure.
func fetchBlockLogs(
	ctx context.Context,
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	chainSup upstreams.ChainSupervisor,
	registry *rating.RatingRegistry,
	block protocol.Block,
) ([]json.RawMessage, string) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_getLogs",
		[]any{map[string]string{"blockHash": block.Hash.ToHexWithPrefix()}},
		chain,
	)
	if err != nil {
		log.Warn().Err(err).Msgf("subengine: failed to build eth_getLogs for block %d on %s", block.Height, chain)
		logsBlocksSkippedMetric.WithLabelValues(chain.String(), "build").Inc()
		return nil, ""
	}

	// Select any available, best-rated upstream whose head is at >= the block's
	// height. A fresh strategy per block carries the height matcher; repeated
	// SelectUpstream calls walk down the rating list (selectedUpstreams dedup).
	strategy := NewRatingStrategy(chain, "eth_getLogs", []Matcher{NewHeightMatcher(int64(block.Height))}, chainSup, registry)

	for attempt := 0; attempt < logsFetchAttempts; attempt++ {
		resp, err := selectAndSend(ctx, supervisor, request, strategy)
		if err != nil {
			// No upstream at this height (or the strategy is exhausted): the block's
			// logs are skipped, so the client silently misses them. Surface it.
			log.Warn().Err(err).Msgf("subengine: no upstream to serve eth_getLogs for block %d on %s; skipping block's logs", block.Height, chain)
			logsBlocksSkippedMetric.WithLabelValues(chain.String(), "no_upstream").Inc()
			return nil, ""
		}
		if resp.Response.HasError() {
			continue // try the next-best upstream
		}
		var arr []json.RawMessage
		if err := sonic.Unmarshal(resp.Response.ResponseResult(), &arr); err != nil {
			log.Warn().Err(err).Msgf("subengine: failed to parse eth_getLogs result for block %d on %s", block.Height, chain)
			logsBlocksSkippedMetric.WithLabelValues(chain.String(), "parse").Inc()
			return nil, ""
		}
		logs := make([]json.RawMessage, 0, len(arr))
		for _, l := range arr {
			logs = append(logs, append(json.RawMessage(nil), l...)) // copy: connector buffers may be pooled
		}
		return logs, resp.UpstreamId
	}
	// Every attempt returned an upstream error: the block's logs are skipped.
	log.Warn().Msgf("subengine: eth_getLogs errored on all %d attempts for block %d on %s; skipping block's logs", logsFetchAttempts, block.Height, chain)
	logsBlocksSkippedMetric.WithLabelValues(chain.String(), "upstream_error").Inc()
	return nil, ""
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
