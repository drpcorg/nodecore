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
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(sidecarBody), 200, protocol.Rest))

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

const (
	sidecarBody     = `{"data":[{"index":"0","blob":"0x00"}]}`
	emptyBlobsBody  = `{"data":[]}`
	blockWithBlobs  = `{"data":{"message":{"slot":"1","body":{"blob_kzg_commitments":["0x01"]}}}}`
	blockNoBlobs    = `{"data":{"message":{"slot":"1","body":{"blob_kzg_commitments":[]}}}}`
	blobDenebFrom   = int64(20)
	blobRetainedAge = int64(60) // blobs retained for slots [60, 100]
)

func mockHead(connector *mocks.ConnectorMock) {
	headBody := `{"data":{"root":"0xaa","header":{"message":{"slot":"` +
		strconv.FormatInt(headSlot, 10) + `","parent_root":"0xbb"}}}}`
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		return r.Method() == "GET#/eth/v1/beacon/headers/head"
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(headBody), 200, protocol.Rest))
}

func mockPreDeneb(connector *mocks.ConnectorMock) {
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot < blobDenebFrom
	})).Return(protocol.NewHttpUpstreamResponseWithError(
		protocol.ResponseErrorWithData(400, "BAD_REQUEST: block is pre-Deneb and has no blobs", nil),
	))
}

// mockRetainedWindow serves slots [blobRetainedAge, head]: even slots carry
// blobs, odd slots carry none (200 {"data":[]} and a block without commitments).
func mockRetainedWindow(connector *mocks.ConnectorMock) {
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot >= blobRetainedAge && slot%2 == 0
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(sidecarBody), 200, protocol.Rest))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot >= blobRetainedAge && slot%2 == 1
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(emptyBlobsBody), 200, protocol.Rest))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blockPathSlot(r)
		return ok && slot >= blobRetainedAge && slot%2 == 1
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(blockNoBlobs), 200, protocol.Rest))
}

func detectBlobBound(t *testing.T, connector *mocks.ConnectorMock) int64 {
	t.Helper()
	detectors := beacon_bounds.NewBeaconChainLowerBoundDetectors(
		"id", chains.GetChain("eth-beacon-chain").Chain, 5*time.Second, connector,
	)
	bounds, err := detectors[3].DetectLowerBound(context.Background())
	require.NoError(t, err)
	require.Len(t, bounds, 1)
	assert.Equal(t, protocol.BlobBound, bounds[0].Type)
	return bounds[0].Bound
}

// TestBeaconBlobLowerBoundEmptyAnswerOnPrunedSlots reproduces a node that keeps
// answering 200 {"data":[]} for slots whose blobs it already pruned. Their blocks
// still carry blob commitments, so those slots must count as a miss and the
// bound must land on the retention window, not on the Deneb fork.
func TestBeaconBlobLowerBoundEmptyAnswerOnPrunedSlots(t *testing.T) {
	connector := mocks.NewConnectorMock()
	mockHead(connector)
	mockPreDeneb(connector)
	mockRetainedWindow(connector)

	// Pruned window [Deneb, retained): empty sidecars, blocks with commitments.
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot >= blobDenebFrom && slot < blobRetainedAge
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(emptyBlobsBody), 200, protocol.Rest))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blockPathSlot(r)
		return ok && slot >= blobDenebFrom && slot < blobRetainedAge
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(blockWithBlobs), 200, protocol.Rest))

	assert.Equal(t, blobRetainedAge, detectBlobBound(t, connector))
}

// TestBeaconBlobLowerBoundInsufficientDataColumns covers PeerDAS nodes that answer
// HTTP 400 "Insufficient data columns to reconstruct blobs" once they no longer
// hold the columns of a slot: that answer is a miss, not a probe error.
func TestBeaconBlobLowerBoundInsufficientDataColumns(t *testing.T) {
	connector := mocks.NewConnectorMock()
	mockHead(connector)
	mockPreDeneb(connector)
	mockRetainedWindow(connector)

	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot >= blobDenebFrom && slot < blobRetainedAge
	})).Return(protocol.NewHttpUpstreamResponseWithError(
		protocol.ResponseErrorWithData(400, "BAD_REQUEST: Insufficient data columns to reconstruct blobs: required 64, but only 0 were found.", nil),
	))

	assert.Equal(t, blobRetainedAge, detectBlobBound(t, connector))
}

// TestBeaconBlobLowerBoundArchive keeps an archive node (every slot since Deneb
// served, including slots without blobs) at the Deneb fork.
func TestBeaconBlobLowerBoundArchive(t *testing.T) {
	connector := mocks.NewConnectorMock()
	mockHead(connector)
	mockPreDeneb(connector)
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot >= blobDenebFrom && slot%2 == 0
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(sidecarBody), 200, protocol.Rest))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blobPathSlot(r)
		return ok && slot >= blobDenebFrom && slot%2 == 1
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(emptyBlobsBody), 200, protocol.Rest))
	connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(r protocol.RequestHolder) bool {
		slot, ok := blockPathSlot(r)
		return ok && slot >= blobDenebFrom && slot%2 == 1
	})).Return(protocol.NewHttpUpstreamResponse("1", []byte(blockNoBlobs), 200, protocol.Rest))

	assert.Equal(t, blobDenebFrom, detectBlobBound(t, connector))
}
