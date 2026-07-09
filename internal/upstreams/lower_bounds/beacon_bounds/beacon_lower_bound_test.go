package beacon_bounds_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/beacon_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	headSlot    = int64(100)
	prunedBelow = int64(40) // slots [40, 100] are retained, [1, 39] pruned
)

func blockPathSlot(r protocol.RequestHolder) (int64, bool) {
	if r.Method() != "GET#/eth/v2/beacon/blocks/*" {
		return 0, false
	}
	rp := r.RequestParams()
	if rp == nil || len(rp.PathParams) != 1 {
		return 0, false
	}
	slot, err := strconv.ParseInt(rp.PathParams[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return slot, true
}

// TestBeaconBlockLowerBoundBinarySearch drives the block detector against a node
// that retains slots [40, 100] and prunes below, asserting the binary search
// converges on 40 and publishes it as a BLOCK bound.
func TestBeaconBlockLowerBoundBinarySearch(t *testing.T) {
	connector := mocks.NewConnectorMock()

	headBody := `{"data":{"root":"0xaa","header":{"message":{"slot":"` +
		strconv.FormatInt(headSlot, 10) + `","parent_root":"0xbb"}}}}`
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/eth/v1/beacon/headers/head"
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(headBody), 200, protocol.Rest))

	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blockPathSlot(r)
		return ok && slot >= prunedBelow
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"data":{"message":{"slot":"1"}}}`), 200, protocol.Rest))

	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blockPathSlot(r)
		return ok && slot < prunedBelow
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"code":404,"message":"NOT_FOUND: beacon block"}`), 200, protocol.Rest))

	detectors := beacon_bounds.NewBeaconChainLowerBoundDetectors(
		"id", chains.GetChain("eth-beacon-chain").Chain, 5*time.Second, connector,
	)
	require.Len(t, detectors, 4)

	blockDetector := detectors[0]
	assert.Equal(t, []protocol.LowerBoundType{protocol.BlockBound}, blockDetector.SupportedTypes())

	bounds, err := blockDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, bounds, 1)
	assert.Equal(t, protocol.BlockBound, bounds[0].Type)
	assert.Equal(t, prunedBelow, bounds[0].Bound)
}

func blobPathSlot(r protocol.RequestHolder) (int64, bool) {
	if r.Method() != "GET#/eth/v1/beacon/blob_sidecars/*" {
		return 0, false
	}
	rp := r.RequestParams()
	if rp == nil || len(rp.PathParams) != 1 {
		return 0, false
	}
	slot, err := strconv.ParseInt(rp.PathParams[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return slot, true
}

// TestBeaconBlobLowerBoundTreatsPreDenebAsMiss reproduces the real-node behaviour
// where pre-Deneb slots answer HTTP 400 "block is pre-Deneb and has no blobs".
// Those slots must classify as below the bound (miss), not as a hard error, so the
// binary search converges on the first slot with retrievable blob sidecars.
func TestBeaconBlobLowerBoundTreatsPreDenebAsMiss(t *testing.T) {
	const denebFrom = int64(60)
	connector := mocks.NewConnectorMock()

	headBody := `{"data":{"root":"0xaa","header":{"message":{"slot":"` +
		strconv.FormatInt(headSlot, 10) + `","parent_root":"0xbb"}}}}`
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/eth/v1/beacon/headers/head"
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(headBody), 200, protocol.Rest))

	// Post-Deneb, retained: 200 with a data array.
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot >= denebFrom
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"data":[]}`), 200, protocol.Rest))

	// Pre-Deneb: HTTP 400 with the pre-Deneb message (surfaced as an error).
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot < denebFrom
	})).Return(protocol.NewHttpUpstreamResponseWithError(
		protocol.ResponseErrorWithData(400, "BAD_REQUEST: block is pre-Deneb and has no blobs", nil),
	))

	detectors := beacon_bounds.NewBeaconChainLowerBoundDetectors(
		"id", chains.GetChain("eth-beacon-chain").Chain, 5*time.Second, connector,
	)
	blobDetector := detectors[3]
	assert.Equal(t, []protocol.LowerBoundType{protocol.BlobBound}, blobDetector.SupportedTypes())

	bounds, err := blobDetector.DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, bounds, 1)
	assert.Equal(t, protocol.BlobBound, bounds[0].Type)
	assert.Equal(t, denebFrom, bounds[0].Bound)
}
