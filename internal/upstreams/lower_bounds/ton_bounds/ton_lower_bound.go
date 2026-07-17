package ton_bounds

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// Whether a liteserver is archival is a deployment property, not something
// that drifts at runtime, so a long re-probe period is enough; we still
// re-probe every tick instead of trusting a one-time answer forever.
const tonPeriod = 15 * time.Minute

// The bound we verify: TON exposes no complete_ledgers equivalent, so we
// probe masterchain seqno 1 and publish it as the lower bound once the node
// proves it can serve it. A non-archival liteserver's sliding window makes
// its real floor unknowable without a binary search (a follow-up), so it
// honestly reports UnknownBound.
const tonVerifiedBound = 1

// Archival lookupBlock for seqno 1 can take ~20s+ while the liteserver digs
// through cold history, so the probe gets at least this much room even when
// the general internal timeout is shorter.
const tonMinProbeTimeout = 30 * time.Second

var tonEmittedBoundTypes = []protocol.LowerBoundType{
	protocol.StateBound,
	protocol.BlockBound,
}

// errTonNotArchival marks a definitive "seqno not in db"-class answer from
// the node: the block is genuinely absent, as opposed to a transport or
// parse failure where the truth is unknown.
var errTonNotArchival = errors.New("ton liteserver does not retain early history")

type TonLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	lastBound atomic.Int64
}

func NewTonLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *TonLowerBoundDetector {
	return &TonLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TonLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	err := t.probeEarlyBlock(ctx)
	if err == nil {
		t.lastBound.Store(tonVerifiedBound)
		return tonBounds(tonVerifiedBound), nil
	}
	if errors.Is(err, errTonNotArchival) {
		// The node answered definitively: seqno 1 is not in its db. Prefer
		// this fresh truth over any previously verified bound - a node can
		// lose its archive (resync onto a pruned snapshot), and re-emitting
		// a stale 1 would misroute history traffic.
		t.lastBound.Store(0)
		log.Warn().Err(err).Msgf(
			"ton upstream '%s' is not archival; emitting UnknownBound",
			t.upstreamId,
		)
		return unknownBound(), nil
	}
	return t.fallback(err), nil
}

func (t *TonLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return append(append([]protocol.LowerBoundType{}, tonEmittedBoundTypes...), protocol.UnknownBound)
}

func (t *TonLowerBoundDetector) Period() time.Duration {
	return tonPeriod
}

// fallback decides what to publish when the probe fails for an
// indeterminate reason (transport, parse). If a previous tick verified the
// bound, re-emit it so the router keeps using the last known good value.
// Otherwise emit UnknownBound=0 so consumers get an explicit "we don't
// know" signal instead of silence.
func (t *TonLowerBoundDetector) fallback(reason error) []protocol.LowerBoundData {
	if cached := t.lastBound.Load(); cached > 0 {
		log.Warn().Err(reason).Msgf(
			"ton upstream '%s' lower-bound probe failed; retaining cached bound=%d",
			t.upstreamId, cached,
		)
		return tonBounds(cached)
	}
	log.Warn().Err(reason).Msgf(
		"ton upstream '%s' lower-bound probe failed and no cache available; emitting UnknownBound",
		t.upstreamId,
	)
	return unknownBound()
}

func tonBounds(bound int64) []protocol.LowerBoundData {
	bounds := make([]protocol.LowerBoundData, 0, len(tonEmittedBoundTypes))
	for _, bt := range tonEmittedBoundTypes {
		bounds = append(bounds, protocol.NewLowerBoundDataNow(bound, bt))
	}
	return bounds
}

func unknownBound() []protocol.LowerBoundData {
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(0, protocol.UnknownBound),
	}
}

// probeEarlyBlock looks up masterchain block seqno 1 to verify the node
// keeps full history. nil means the block is present and the verified bound
// can be published; errTonNotArchival means the node definitively lacks it.
func (t *TonLowerBoundDetector) probeEarlyBlock(ctx context.Context) error {
	timeout := t.internalTimeout
	if timeout < tonMinProbeTimeout {
		timeout = tonMinProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest(
		"GET#/lookupBlock",
		&protocol.RequestParams{
			QueryParams: map[string][]string{
				"workchain": {"-1"},
				"shard":     {"-9223372036854775808"},
				"seqno":     {"1"},
			},
		},
		t.chain,
	)

	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if isTonNotInDbMessage(respErr.Message) {
			return fmt.Errorf("block %d lookup on upstream '%s' answered %q: %w",
				tonVerifiedBound, t.upstreamId, respErr.Message, errTonNotArchival)
		}
		return fmt.Errorf("cannot lookup ton block %d: %w", tonVerifiedBound, respErr)
	}

	raw := response.ResponseResult()
	if len(raw) == 0 {
		return fmt.Errorf("ton upstream '%s' returned an empty body for block %d", t.upstreamId, tonVerifiedBound)
	}
	var envelope tonEnvelope
	if err := sonic.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("ton upstream '%s' block %d lookup unparseable: %w", t.upstreamId, tonVerifiedBound, err)
	}
	if !envelope.Ok {
		if isTonNotInDbMessage(envelope.Error) {
			return fmt.Errorf("block %d lookup on upstream '%s' answered %q: %w",
				tonVerifiedBound, t.upstreamId, envelope.Error, errTonNotArchival)
		}
		return fmt.Errorf("ton upstream '%s' block %d lookup failed: %s", t.upstreamId, tonVerifiedBound, envelope.Error)
	}
	return nil
}

// isTonNotInDbMessage recognises the toncenter/liteserver "history miss"
// answers: `LITE_SERVER_NOTREADY: ... seqno not in db` and friends. These
// are definitive - the node was reached and said it lacks the block.
func isTonNotInDbMessage(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "not in db") || strings.Contains(lower, "lite_server")
}

type tonEnvelope struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
}

var _ lower_bounds.LowerBoundDetector = (*TonLowerBoundDetector)(nil)
