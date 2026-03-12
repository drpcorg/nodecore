package lower_bounds_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func matchFirstAvailableBlockRequest(request protocol.RequestHolder) bool {
	body, err := request.Body()
	if err != nil {
		return false
	}

	return request.Method() == "getFirstAvailableBlock" &&
		request.Id() == "1" &&
		request.RequestType() == protocol.JsonRpc &&
		strings.Contains(string(body), `"method":"getFirstAvailableBlock"`) &&
		strings.Contains(string(body), `"params":null`)
}

func matchGetBlockRequest(slot int64) func(protocol.RequestHolder) bool {
	return func(request protocol.RequestHolder) bool {
		body, err := request.Body()
		if err != nil {
			return false
		}

		bodyStr := string(body)
		return request.Method() == "getBlock" &&
			request.Id() == "1" &&
			request.RequestType() == protocol.JsonRpc &&
			strings.Contains(bodyStr, `"method":"getBlock"`) &&
			strings.Contains(bodyStr, `"params":[`+strconv.FormatInt(slot, 10)) &&
			strings.Contains(bodyStr, `"showRewards":false`) &&
			strings.Contains(bodyStr, `"transactionDetails":"none"`) &&
			strings.Contains(bodyStr, `"maxSupportedTransactionVersion":0`)
	}
}

func TestSolanaLowerBoundDetectorSupportedTypes(t *testing.T) {
	detector := lower_bounds.NewSolanaLowerBoundDetector("id", time.Second, mocks.NewConnectorMock())

	assert.Equal(t, []protocol.LowerBoundType{protocol.SlotBound, protocol.StateBound}, detector.SupportedTypes())
}

func TestSolanaLowerBoundDetectorPeriod(t *testing.T) {
	detector := lower_bounds.NewSolanaLowerBoundDetector("id", time.Second, mocks.NewConnectorMock())

	assert.Equal(t, 3*time.Minute, detector.Period())
}

func TestSolanaLowerBoundDetectorDetectLowerBound(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchFirstAvailableBlockRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`123`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchGetBlockRequest(123))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"blockHeight":456}`), protocol.JsonRpc)).
		Once()

	detector := lower_bounds.NewSolanaLowerBoundDetector("id", time.Second, connector)

	result, err := detector.DetectLowerBound()

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, protocol.SlotBound, result[0].Type)
	assert.Equal(t, int64(123), result[0].Bound)
	assert.Equal(t, protocol.StateBound, result[1].Type)
	assert.Equal(t, int64(456), result[1].Bound)

	connector.AssertExpectations(t)
}

func TestSolanaLowerBoundDetectorDetectLowerBoundDoesNotReturnZeroValues(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchFirstAvailableBlockRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`0`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchGetBlockRequest(1))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"blockHeight":0}`), protocol.JsonRpc)).
		Once()

	detector := lower_bounds.NewSolanaLowerBoundDetector("id", time.Second, connector)

	result, err := detector.DetectLowerBound()

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, protocol.SlotBound, result[0].Type)
	assert.Equal(t, int64(1), result[0].Bound)
	assert.Equal(t, protocol.StateBound, result[1].Type)
	assert.Equal(t, int64(1), result[1].Bound)

	connector.AssertExpectations(t)
}

func TestSolanaLowerBoundDetectorDetectLowerBoundRetriesAfterFirstAvailableBlockError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchFirstAvailableBlockRequest)).
		Return(protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchFirstAvailableBlockRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`123`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchGetBlockRequest(123))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"blockHeight":456}`), protocol.JsonRpc)).
		Once()

	detector := lower_bounds.NewSolanaLowerBoundDetector("id", time.Second, connector)

	result, err := detector.DetectLowerBound()

	assert.NoError(t, err)
	assert.Equal(t, []protocol.LowerBoundType{protocol.SlotBound, protocol.StateBound}, []protocol.LowerBoundType{result[0].Type, result[1].Type})
	assert.Equal(t, int64(123), result[0].Bound)
	assert.Equal(t, int64(456), result[1].Bound)
	connector.AssertExpectations(t)
}

func TestSolanaLowerBoundDetectorDetectLowerBoundRetriesAfterGetBlockError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchFirstAvailableBlockRequest)).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`123`), protocol.JsonRpc)).
		Twice()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchGetBlockRequest(123))).
		Return(protocol.NewReplyError("1", protocol.ServerError(), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchGetBlockRequest(123))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"blockHeight":456}`), protocol.JsonRpc)).
		Once()

	detector := lower_bounds.NewSolanaLowerBoundDetector("id", time.Second, connector)

	result, err := detector.DetectLowerBound()

	assert.NoError(t, err)
	assert.Equal(t, []protocol.LowerBoundType{protocol.SlotBound, protocol.StateBound}, []protocol.LowerBoundType{result[0].Type, result[1].Type})
	assert.Equal(t, int64(123), result[0].Bound)
	assert.Equal(t, int64(456), result[1].Bound)
	connector.AssertExpectations(t)
}
