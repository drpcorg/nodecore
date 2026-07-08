package tron_bounds_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/tron_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func tronOK(body string) protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(body), protocol.Rest)
}

func tronBlockJSON(num int64) string {
	return fmt.Sprintf(
		`{"blockID":"%064x","block_header":{"raw_data":{"number":%d,"timestamp":1700000000000}}}`,
		num, num,
	)
}

func tronEmptyBlock() string {
	return `{}`
}

// fastTron shrinks retry backoff so error-path tests stay quick.
func fastTron(d *tron_bounds.TronLowerBoundDetector) *tron_bounds.TronLowerBoundDetector {
	d.SetSearchRetryPolicy(3, time.Millisecond, time.Millisecond)
	return d
}

// matchTronRequest verifies the request is a POST to /wallet/getblock with
// an exact body (use "" to match a nil/empty body for the "latest" fetch).
func matchTronRequest(expectedBody string) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		if req.Method() != "POST#/wallet/getblock" {
			return false
		}
		if req.RequestType() != protocol.Rest {
			return false
		}
		body, err := req.Body()
		if err != nil {
			return false
		}
		return string(body) == expectedBody
	}
}

// tronProbeHeight extracts the requested block height from a /wallet/getblock probe body.
func tronProbeHeight(req protocol.RequestHolder) (int64, bool) {
	if req.Method() != "POST#/wallet/getblock" {
		return 0, false
	}
	body, err := req.Body()
	if err != nil {
		return 0, false
	}
	node, err := sonic.Get(body, "id_or_num")
	if err != nil {
		return 0, false
	}
	raw, err := node.String()
	if err != nil {
		return 0, false
	}
	h, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return h, true
}

func matchTronAtLeast(threshold int64) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		h, ok := tronProbeHeight(req)
		return ok && h >= threshold
	}
}

func matchTronBelow(threshold int64) func(protocol.RequestHolder) bool {
	return func(req protocol.RequestHolder) bool {
		h, ok := tronProbeHeight(req)
		return ok && h < threshold
	}
}

func TestTronLowerBoundDetector_SupportedTypesAndPeriod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	assert.ElementsMatch(t,
		[]protocol.LowerBoundType{
			protocol.BlockBound,
			protocol.StateBound,
			protocol.TxBound,
			protocol.ReceiptsBound,
		},
		detector.SupportedTypes(),
	)
	assert.Equal(t, 3*time.Minute, detector.Period())
}

func TestTronLowerBoundDetector_FansOutOneSearchToAllFourBoundTypes(t *testing.T) {
	connector := mocks.NewConnectorMock()
	// latest = 5; blocks >= 3 present -> expected bound = 3.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(5))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronAtLeast(3))).
		Return(tronOK(tronBlockJSON(3))).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronBelow(3))).
		Return(tronOK(tronEmptyBlock())).
		Maybe()

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	result, err := detector.DetectLowerBound()
	require.NoError(t, err)

	// One search returns one bound, the detector fans it out to all four types.
	require.Len(t, result, 4)
	got := make(map[protocol.LowerBoundType]int64, len(result))
	for _, b := range result {
		got[b.Type] = b.Bound
	}
	assert.Equal(t, int64(3), got[protocol.BlockBound])
	assert.Equal(t, int64(3), got[protocol.StateBound])
	assert.Equal(t, int64(3), got[protocol.TxBound])
	assert.Equal(t, int64(3), got[protocol.ReceiptsBound])
}

func TestTronLowerBoundDetector_AllAvailableReturnsOneForAllTypes(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(100))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronAtLeast(0))).
		Return(tronOK(tronBlockJSON(1))).
		Maybe()

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	result, err := detector.DetectLowerBound()
	require.NoError(t, err)
	require.Len(t, result, 4)
	for _, b := range result {
		assert.Equal(t, int64(1), b.Bound)
	}
}

func TestTronLowerBoundDetector_LatestEmptyBodyFails(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronEmptyBlock()))

	detector := fastTron(tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector))

	_, err := detector.DetectLowerBound()
	require.Error(t, err)
}

func TestTronLowerBoundDetector_LatestConnectorErrorPropagates(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := fastTron(tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector))

	_, err := detector.DetectLowerBound()
	require.Error(t, err)
}

func TestTronLowerBoundDetector_EmptyBodyOnProbeMeansBlockMissing(t *testing.T) {
	// Latest = 2, blocks below 2 missing, height 2 present → bound is 2.
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(2))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronAtLeast(2))).
		Return(tronOK(tronBlockJSON(2))).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronBelow(2))).
		Return(tronOK(tronEmptyBlock())).
		Maybe()

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	result, err := detector.DetectLowerBound()
	require.NoError(t, err)
	require.Len(t, result, 4)
	for _, b := range result {
		assert.Equal(t, int64(2), b.Bound)
	}
}

func TestTronLowerBoundDetector_BodyWithoutBlockIDCountsAsMissing(t *testing.T) {
	// A malformed payload (no blockID) is treated as missing — defensive
	// against unexpected error envelopes that don't trip HasError().
	// Latest=2, blocks below 2 return {"Error":"..."}, height 2 returns a real block.
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(2))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronAtLeast(2))).
		Return(tronOK(tronBlockJSON(2))).
		Maybe()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronBelow(2))).
		Return(tronOK(`{"Error":"validate signature error"}`)).
		Maybe()

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	result, err := detector.DetectLowerBound()
	require.NoError(t, err)
	require.Len(t, result, 4)
	for _, b := range result {
		assert.Equal(t, int64(2), b.Bound)
	}
}
