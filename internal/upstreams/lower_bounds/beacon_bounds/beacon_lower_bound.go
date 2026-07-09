package beacon_bounds

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	beaconBoundPeriod = 5 * time.Minute
	// beaconBoundMaxOffset mirrors evmLowerBoundMaxOffset: beacon nodes can miss
	// sporadic slots (skipped/orphaned), so the search shifts left up to this many
	// slots below a no-data probe before treating the window as pruned.
	beaconBoundMaxOffset = 20
	slotsPerEpoch        = 32
)

// NewBeaconChainLowerBoundDetectors returns the four data-availability detectors
// dshackle's BeaconChainLowerBoundService exposes: retained block, state, epoch
// (attestation rewards) and blob-sidecar slots. Each drives the generic
// binary-search calculator with a REST probe; the "availability" signal is the
// node still serving the slot (HTTP 200) versus a pruned/not-found response, so
// the predicate stays monotonic as the binary search requires.
func NewBeaconChainLowerBoundDetectors(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) []lower_bounds.LowerBoundDetector {
	prober := &beaconProber{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
	return []lower_bounds.LowerBoundDetector{
		newBlockDetector(upstreamId, prober),
		newStateDetector(upstreamId, prober),
		newEpochDetector(upstreamId, prober),
		newBlobDetector(upstreamId, prober),
	}
}

var _ lower_bounds.LowerBoundDetector = (*beaconLowerBoundDetector)(nil)

// beaconLowerBoundDetector adapts a LowerBoundSearchCalculator + a probe/fetcher
// pair to the LowerBoundDetector interface.
type beaconLowerBoundDetector struct {
	calculator  *lower_bounds.LowerBoundSearchCalculator
	fetchLatest lower_bounds.LowerBoundLatestHeightFetcher
	probe       lower_bounds.LowerBoundProbe
}

func (d *beaconLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	return d.calculator.DetectLowerBound(ctx, d.fetchLatest, d.probe)
}

func (d *beaconLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return d.calculator.SupportedTypes()
}

func (d *beaconLowerBoundDetector) Period() time.Duration {
	return d.calculator.Period()
}

func newBlockDetector(upstreamId string, p *beaconProber) *beaconLowerBoundDetector {
	notFound := []string{"could not find requested block", "has not been found", "lock not found", "internal server error"}
	return &beaconLowerBoundDetector{
		calculator:  lower_bounds.NewLowerBoundSearchCalculatorWithOffset(upstreamId, protocol.BlockBound, []protocol.LowerBoundType{protocol.BlockBound}, beaconBoundPeriod, beaconBoundMaxOffset),
		fetchLatest: p.fetchHeadSlot,
		probe: func(ctx context.Context, slot int64) (bool, error) {
			req := protocol.NewInternalUpstreamRestRequest(
				"GET#/eth/v2/beacon/blocks/*",
				&protocol.RequestParams{PathParams: []string{strconv.FormatInt(slot, 10)}},
				p.chain,
			)
			return p.doProbe(ctx, req, notFound, hasDataKey)
		},
	}
}

func newStateDetector(upstreamId string, p *beaconProber) *beaconLowerBoundDetector {
	notFound := []string{"could not get requested state", "missing state"}
	return &beaconLowerBoundDetector{
		calculator:  lower_bounds.NewLowerBoundSearchCalculatorWithOffset(upstreamId, protocol.StateBound, []protocol.LowerBoundType{protocol.StateBound}, beaconBoundPeriod, beaconBoundMaxOffset),
		fetchLatest: p.fetchHeadSlot,
		probe: func(ctx context.Context, slot int64) (bool, error) {
			req := protocol.NewInternalUpstreamRestRequestWithBody(
				"POST#/eth/v1/beacon/states/*/validator_balances",
				&protocol.RequestParams{PathParams: []string{strconv.FormatInt(slot, 10)}},
				[]byte(`["1"]`),
				p.chain,
			)
			return p.doProbe(ctx, req, notFound, hasDataKey)
		},
	}
}

// newEpochDetector searches in epoch units (slot / 32). The latest height and the
// resulting bound are epoch numbers; the probe converts an epoch back to its first
// slot to query attestation rewards.
func newEpochDetector(upstreamId string, p *beaconProber) *beaconLowerBoundDetector {
	notFound := []string{"could not get requested state", "missing state", "could not find requested block"}
	return &beaconLowerBoundDetector{
		calculator: lower_bounds.NewLowerBoundSearchCalculatorWithOffset(upstreamId, protocol.EpochBound, []protocol.LowerBoundType{protocol.EpochBound}, beaconBoundPeriod, beaconBoundMaxOffset),
		fetchLatest: func(ctx context.Context) (int64, error) {
			slot, err := p.fetchHeadSlot(ctx)
			if err != nil {
				return 0, err
			}
			return slot / slotsPerEpoch, nil
		},
		probe: func(ctx context.Context, epoch int64) (bool, error) {
			req := protocol.NewInternalUpstreamRestRequestWithBody(
				"POST#/eth/v1/beacon/rewards/attestations/*",
				&protocol.RequestParams{PathParams: []string{strconv.FormatInt(epoch, 10)}},
				[]byte(`["1"]`),
				p.chain,
			)
			return p.doProbe(ctx, req, notFound, hasDataKey)
		},
	}
}

func newBlobDetector(upstreamId string, p *beaconProber) *beaconLowerBoundDetector {
	// Pre-Deneb slots answer HTTP 400 "block is pre-Deneb and has no blobs";
	// they sit below the blob-availability bound, so they must count as a miss
	// rather than a hard error - otherwise the binary search never converges.
	notFound := []string{"lock not found", "has not been found", "pre-deneb", "no blobs"}
	return &beaconLowerBoundDetector{
		calculator:  lower_bounds.NewLowerBoundSearchCalculatorWithOffset(upstreamId, protocol.BlobBound, []protocol.LowerBoundType{protocol.BlobBound}, beaconBoundPeriod, beaconBoundMaxOffset),
		fetchLatest: p.fetchHeadSlot,
		probe: func(ctx context.Context, slot int64) (bool, error) {
			req := protocol.NewInternalUpstreamRestRequest(
				"GET#/eth/v1/beacon/blob_sidecars/*",
				&protocol.RequestParams{PathParams: []string{strconv.FormatInt(slot, 10)}},
				p.chain,
			)
			// A pruned slot answers 404; a retained slot answers 200 even when it
			// carries no blobs, so a present "data" key (empty array included) counts
			// as available - this keeps the availability predicate monotonic.
			return p.doProbe(ctx, req, notFound, hasDataKey)
		},
	}
}

type beaconProber struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func (p *beaconProber) fetchHeadSlot(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, p.internalTimeout)
	defer cancel()

	req := protocol.NewInternalUpstreamRestRequest("GET#/eth/v1/beacon/headers/head", nil, p.chain)
	resp := p.connector.SendRequest(ctx, req)
	if resp.HasError() {
		return 0, resp.GetError()
	}
	var header beaconHeaderResponse
	if err := sonic.Unmarshal(resp.ResponseResult(), &header); err != nil {
		return 0, fmt.Errorf("beacon upstream '%s' head header unparseable: %w", p.upstreamId, err)
	}
	slot, err := strconv.ParseInt(header.Data.Header.Message.Slot, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("beacon upstream '%s' head slot unparseable: %w", p.upstreamId, err)
	}
	return slot, nil
}

// doProbe issues a probe request and classifies the reply as available (true),
// pruned/not-found (false, nil) or a transient error (false, err). Beacon nodes
// report "not found" both as an HTTP 404 and as a 200 body carrying an error
// object, so both shapes are inspected.
func (p *beaconProber) doProbe(
	ctx context.Context,
	req protocol.RequestHolder,
	notFoundHints []string,
	hit func([]byte) (bool, error),
) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.internalTimeout)
	defer cancel()

	resp := p.connector.SendRequest(ctx, req)
	if resp.HasError() {
		respErr := resp.GetError()
		if respErr == nil {
			return false, fmt.Errorf("beacon upstream '%s' probe failed with no error detail", p.upstreamId)
		}
		if resp.ResponseCode() == 404 || matchesAnyHint(respErr.Message, notFoundHints) {
			return false, nil
		}
		return false, respErr
	}

	raw := resp.ResponseResult()
	if len(raw) == 0 {
		return false, fmt.Errorf("beacon upstream '%s' probe returned an empty body", p.upstreamId)
	}
	if code, msg, ok := parseBeaconError(raw); ok {
		if code == 404 || matchesAnyHint(msg, notFoundHints) {
			return false, nil
		}
		return false, fmt.Errorf("beacon upstream '%s' probe error %d: %s", p.upstreamId, code, msg)
	}
	return hit(raw)
}

type beaconHeaderResponse struct {
	Data struct {
		Header struct {
			Message struct {
				Slot string `json:"slot"`
			} `json:"message"`
		} `json:"header"`
	} `json:"data"`
}

// hasDataKey succeeds whenever a well-formed "data" payload is present. A pruned
// slot is already filtered out by the 404/error path in doProbe, so a retained
// slot answering 200 with a "data" key (an empty blob array included) counts as
// available - keeping the availability predicate monotonic for the binary search.
func hasDataKey(raw []byte) (bool, error) {
	node, err := sonic.Get(raw, "data")
	if err != nil {
		return false, nil
	}
	return node.Exists(), nil
}

// parseBeaconError detects a beacon error envelope ({"code":404,"message":"..."})
// carried in a 200 response body.
func parseBeaconError(raw []byte) (int, string, bool) {
	var probe struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if err := sonic.Unmarshal(raw, &probe); err != nil {
		return 0, "", false
	}
	if probe.Code == nil {
		return 0, "", false
	}
	return *probe.Code, probe.Message, true
}

func matchesAnyHint(msg string, hints []string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	for _, hint := range hints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}
