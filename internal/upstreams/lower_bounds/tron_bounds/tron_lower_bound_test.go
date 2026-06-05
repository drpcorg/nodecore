package tron_bounds_test

import (
	"fmt"
	"testing"
	"time"

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
	// latest = 5; expected bound = 3.
	// Probe order under sort.Search([2,5]): 1 (initial), 4, 3, 2.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(5))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"1","detail":false}`))).
		Return(tronOK(tronEmptyBlock())).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"4","detail":false}`))).
		Return(tronOK(tronBlockJSON(4))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"3","detail":false}`))).
		Return(tronOK(tronBlockJSON(3))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"2","detail":false}`))).
		Return(tronOK(tronEmptyBlock())).
		Once()

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

	connector.AssertExpectations(t)
}

func TestTronLowerBoundDetector_HeightOneAvailableReturnsOneForAllTypes(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(100))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"1","detail":false}`))).
		Return(tronOK(tronBlockJSON(1))).
		Once()

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	result, err := detector.DetectLowerBound()
	require.NoError(t, err)
	require.Len(t, result, 4)
	for _, b := range result {
		assert.Equal(t, int64(1), b.Bound)
	}
	connector.AssertExpectations(t)
}

func TestTronLowerBoundDetector_LatestEmptyBodyFails(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronEmptyBlock()))

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	_, err := detector.DetectLowerBound()
	require.Error(t, err)
}

func TestTronLowerBoundDetector_LatestConnectorErrorPropagates(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	_, err := detector.DetectLowerBound()
	require.Error(t, err)
}

func TestTronLowerBoundDetector_EmptyBodyOnProbeMeansBlockMissing(t *testing.T) {
	// Latest = 2, height 1 missing, height 2 present → bound is 2.
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(2))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"1","detail":false}`))).
		Return(tronOK(tronEmptyBlock())).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"2","detail":false}`))).
		Return(tronOK(tronBlockJSON(2))).
		Once()

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	result, err := detector.DetectLowerBound()
	require.NoError(t, err)
	require.Len(t, result, 4)
	for _, b := range result {
		assert.Equal(t, int64(2), b.Bound)
	}
	connector.AssertExpectations(t)
}

func TestTronLowerBoundDetector_BodyWithoutBlockIDCountsAsMissing(t *testing.T) {
	// A malformed payload (no blockID) is treated as missing — defensive
	// against unexpected error envelopes that don't trip HasError().
	// Latest=2, height 1 returns {"Error":"..."}, height 2 returns a real block.
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(""))).
		Return(tronOK(tronBlockJSON(2))).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"1","detail":false}`))).
		Return(tronOK(`{"Error":"validate signature error"}`)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchTronRequest(`{"id_or_num":"2","detail":false}`))).
		Return(tronOK(tronBlockJSON(2))).
		Once()

	detector := tron_bounds.NewTronLowerBoundDetector("id", chains.TRON, time.Second, connector)

	result, err := detector.DetectLowerBound()
	require.NoError(t, err)
	require.Len(t, result, 4)
	for _, b := range result {
		assert.Equal(t, int64(2), b.Bound)
	}
	connector.AssertExpectations(t)
}
