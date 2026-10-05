package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	"github.com/google/uuid"
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

// logsNotReadyRetriesMetric counts rounds over the upstreams repeated because
// they reported the block as not ready yet (see blockNotReady).
var logsNotReadyRetriesMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "logs_source",
		Name:      "not_ready_retries_total",
		Help:      "The total number of times eth_getLogs for a block was asked again because the upstreams reported it as not ready yet",
	},
	[]string{"chain"},
)

func init() {
	prometheus.MustRegister(logsBlocksSkippedMetric, logsNotReadyRetriesMetric)
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
	// A block the upstream reports as not ready yet is asked again with a backoff
	// from logsNotReadyStep up to logsNotReadyMaxStep, until one block time after
	// its head arrived, clamped to [logsNotReadyWaitMin, logsNotReadyWaitMax].
	logsNotReadyStep    = 100 * time.Millisecond
	logsNotReadyMaxStep = time.Second
	logsNotReadyWaitMin = time.Second
	logsNotReadyWaitMax = 3 * time.Second
)

// notReadyErrors are upstream answers for a block the upstream has announced but
// cannot serve logs of yet: erigon dispatches newHeads before it commits the
// block, cosmos-evm nodes index the block hash after the header event.
var notReadyErrors = []string{
	"block range extends beyond current head block", // erigon: number past the executed head
	"beyond latest executed block",                  // erigon: hash not executed yet
	"block not found",                               // erigon, cosmos-evm ("block not found for hash")
	"unknown block",                                 // geth
	"header not found",                              // geth, reth
	"could not find results for height",             // cosmos-evm: tendermint block results
}

// blockNotReady reports whether err says the block is not available yet.
func blockNotReady(err *protocol.ResponseError) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Message)
	for _, s := range notReadyErrors {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// logsNotReadyWait is how long after its head arrived a block the upstream
// reports as not ready yet is asked again.
func logsNotReadyWait(chain chains.Chain) time.Duration {
	return min(max(chains.GetChain(chain.String()).Settings.ExpectedBlockTime, logsNotReadyWaitMin), logsNotReadyWaitMax)
}

// newLogsSourceBuilder builds the chain's single shared "all logs" source: for
// each new block it issues one eth_getLogs{blockHash} (no address/topic filter)
// and emits every log as its own event; per-client address/topic filtering
// happens in the processor. Upstream selection is by the block's HEIGHT (any
// available upstream at >= that height), not by the head producer, so a producer
// that has since gone away does not break log delivery.
//
// Blocks are announced from a head feed filtered to the upstreams that can serve
// them (available, with LogsCap, matching the client's selectors), not from the
// chain's merged head: that one may come from an upstream without eth_getLogs,
// and its block would be skipped because no upstream that has the method had
// reached it yet. Heights that head jumps over, and the new chain after a reorg,
// are fetched by parent hash.
//
// Reorgs are handled via the block-update stream (see subengine.StreamBlockUpdates):
// a dropped block's cached logs are re-emitted with removed:true. The source
// terminates with a terminal frame (clients resubscribe and are resolved afresh)
// when the feed goes empty: no upstream passes the filter any more.
func newLogsSourceBuilder(
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	registry *rating.RatingRegistry,
	filter upstreams.FilterUpstream,
) subengine.SourceBuilder {
	return func(srcCtx context.Context) (*subengine.Source, error) {
		chainSup := supervisor.GetChainSupervisor(chain)
		if chainSup == nil {
			return nil, protocol.NoAvailableUpstreamsError()
		}

		notReadyWait := logsNotReadyWait(chain)

		out := make(chan protocol.SubResponse, logsBufferSize)
		updates := make(chan subengine.BlockUpdate, 64)

		feed := chainSup.SubscribeHead(fmt.Sprintf("subengine_logs_%s_%s", chain, uuid.NewString()), filter)
		go subengine.StreamBlockUpdates(srcCtx, chain, feed, updates, blockByHashResolver(supervisor, chain, chainSup, registry))

		go func() {
			defer close(out)
			cache := newLogCache(logsCacheSize)

			for {
				select {
				case <-srcCtx.Done():
					return
				case update, ok := <-updates:
					if !ok {
						// the feed went empty: no upstream can serve logs any more
						if srcCtx.Err() == nil {
							out <- &protocol.GenericSubResponse{Error: protocol.SubscribeTotalFailureError()}
						}
						return
					}
					switch update.Kind {
					case subengine.BlockNew:
						logs, upstreamId := fetchBlockLogs(srcCtx, supervisor, chain, chainSup, registry, update.Block, update.Seen.Add(notReadyWait))
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

// fetchBlockLogs returns the raw log objects of block, fetched via eth_getLogs on
// an upstream chosen by height, plus the serving upstream id. It returns (nil,"")
// when the block cannot be served (no upstream at the height, or every attempt
// errored); the source treats that as a skipped block, not a terminal failure.
// When the upstreams that were asked only reported the block as not ready yet,
// they are asked again, with a backoff, until notReadyUntil.
func fetchBlockLogs(
	ctx context.Context,
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	chainSup upstreams.ChainSupervisor,
	registry *rating.RatingRegistry,
	block protocol.Block,
	notReadyUntil time.Time,
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
	// height. A fresh strategy carries the height matcher; repeated SelectUpstream
	// calls walk down the rating list (selectedUpstreams dedup).
	// upstreams that answered another error are not asked again in later rounds
	var failed []string
	newStrategy := func() *RatingStrategy {
		strategy := NewRatingStrategy(chain, "eth_getLogs", []Matcher{NewHeightMatcher(int64(block.Height))}, chainSup, registry)
		for _, id := range failed {
			strategy.selectedUpstreams.Add(id)
		}
		return strategy
	}
	strategy := newStrategy()
	wait := logsNotReadyStep
	var lastErr *protocol.ResponseError
	var lastUpstream string
	notReady := false // an upstream asked in this round reported the block as not ready yet

	for attempt := 0; attempt < logsFetchAttempts; {
		resp, err := selectAndSend(ctx, supervisor, request, strategy)
		if err != nil {
			if notReady && time.Now().Before(notReadyUntil) {
				// every upstream at the height was asked and none could serve the
				// block yet: ask again shortly, from the top of the rating list
				select {
				case <-ctx.Done():
					return nil, ""
				case <-time.After(min(wait, time.Until(notReadyUntil))):
				}
				wait = min(2*wait, logsNotReadyMaxStep)
				strategy, notReady = newStrategy(), false
				logsNotReadyRetriesMetric.WithLabelValues(chain.String()).Inc()
				continue
			}
			reason := "no_upstream"
			if notReady {
				reason = "not_ready"
			}
			// No upstream at this height (or the strategy is exhausted): the block's
			// logs are skipped, so the client silently misses them. Surface it.
			log.Warn().Err(err).Str("upstream", lastUpstream).Str("upstream_error", errorMessage(lastErr)).
				Msgf("subengine: no upstream to serve eth_getLogs for block %d on %s; skipping block's logs", block.Height, chain)
			logsBlocksSkippedMetric.WithLabelValues(chain.String(), reason).Inc()
			return nil, ""
		}
		if resp.Response.HasError() {
			lastErr, lastUpstream = resp.Response.GetError(), resp.UpstreamId
			if blockNotReady(lastErr) {
				notReady = true
			} else {
				failed = append(failed, resp.UpstreamId)
				attempt++
			}
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
	log.Warn().Str("upstream", lastUpstream).Str("upstream_error", errorMessage(lastErr)).
		Msgf("subengine: eth_getLogs errored on all %d attempts for block %d on %s; skipping block's logs", logsFetchAttempts, block.Height, chain)
	logsBlocksSkippedMetric.WithLabelValues(chain.String(), "upstream_error").Inc()
	return nil, ""
}

func errorMessage(err *protocol.ResponseError) string {
	if err == nil {
		return ""
	}
	return err.Message
}

// blockByHashResolver lets the block-update stream fetch the head's ancestors by
// hash: heights the head jumped over, or the new chain after a reorg.
func blockByHashResolver(
	supervisor upstreams.UpstreamSupervisor,
	chain chains.Chain,
	chainSup upstreams.ChainSupervisor,
	registry *rating.RatingRegistry,
) subengine.BlockResolver {
	return func(ctx context.Context, hash blockchain.HashId, height uint64) (protocol.Block, error) {
		request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBlockByHash", []any{hash.ToHexWithPrefix(), false}, chain)
		if err != nil {
			return protocol.Block{}, err
		}
		strategy := NewRatingStrategy(chain, "eth_getBlockByHash", []Matcher{NewHeightMatcher(int64(height))}, chainSup, registry)
		for attempt := 0; attempt < logsFetchAttempts; attempt++ {
			resp, err := selectAndSend(ctx, supervisor, request, strategy)
			if err != nil {
				return protocol.Block{}, err
			}
			if resp.Response.HasError() {
				continue
			}
			var header struct {
				Hash   string           `json:"hash"`
				Parent string           `json:"parentHash"`
				Number *rpc.BlockNumber `json:"number"`
			}
			// null: this upstream does not know the block
			if err := sonic.Unmarshal(resp.Response.ResponseResult(), &header); err != nil || header.Number == nil {
				continue
			}
			block := protocol.Block{
				Height:     uint64(header.Number.Int64()),
				Hash:       blockchain.NewHashIdFromString(header.Hash),
				ParentHash: blockchain.NewHashIdFromString(header.Parent),
			}
			if block.Hash.Equals(hash) {
				return block, nil
			}
		}
		return protocol.Block{}, fmt.Errorf("no upstream returned block %s", hash.ToHexWithPrefix())
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
