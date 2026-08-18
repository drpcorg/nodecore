package eth_labels_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func matchAllMidsInfoRequest(request protocol.RequestHolder) bool {
	if request == nil || request.Method() != "POST#/info" || request.RequestType() != protocol.Rest {
		return false
	}
	body, err := request.Body()
	if err != nil {
		return false
	}
	return string(body) == `{"type":"allMids"}`
}

func TestEthAllInfoLabelsDetectorReturnsNilForNonHyperliquidChain(t *testing.T) {
	connector := mocks.NewConnectorMock()
	detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.ETHEREUM, time.Second, connector)

	result := detector.DetectLabels()

	assert.Nil(t, result)
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestEthAllInfoLabelsDetectorReturnsNilWhenConnectorIsNil(t *testing.T) {
	detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, nil)

	assert.Nil(t, detector.DetectLabels())
}

func TestEthAllInfoLabelsDetectorReturnsAllInfoWhenInfoSucceeds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAllMidsInfoRequest)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"BTC":"1"}`), 200, protocol.Rest)).
		Once()

	detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{"allInfo": "true", "partialInfo": "false"}, result)
	connector.AssertExpectations(t)
}

func TestEthAllInfoLabelsDetectorReturnsPartialInfoWhenInfoErrors(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAllMidsInfoRequest)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`Failed to deserialize the JSON body into the target type`), 422, protocol.Rest)).
		Once()

	detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{"allInfo": "false", "partialInfo": "true"}, result)
	connector.AssertExpectations(t)
}

func TestEthAllInfoLabelsDetectorLabelConstantsMatchEmittedKeys(t *testing.T) {
	assert.Equal(t, "allInfo", eth_labels.AllInfoLabel)
	assert.Equal(t, "partialInfo", eth_labels.PartialInfoLabel)
}

// the two labels are mutually exclusive on every detection, so a routing rule
// on either one always matches a disjoint set of upstreams
func TestEthAllInfoLabelsDetectorLabelsAreAlwaysComplementary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *protocol.GenericUpstreamResponse
	}{
		{"ok", protocol.NewHttpUpstreamResponse("1", []byte(`{"BTC":"1"}`), 200, protocol.Rest)},
		{"unprocessable", protocol.NewHttpUpstreamResponse("1", []byte(`nope`), 422, protocol.Rest)},
		{"server error", protocol.NewHttpUpstreamResponse("1", []byte(`boom`), 500, protocol.Rest)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(matchAllMidsInfoRequest)).
				Return(tc.response).
				Once()

			detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

			result := detector.DetectLabels()

			assert.Len(t, result, 2)
			assert.NotEqual(t, result[eth_labels.AllInfoLabel], result[eth_labels.PartialInfoLabel])
			connector.AssertExpectations(t)
		})
	}
}

func TestEthAllInfoLabelsDetectorDetectsOnEveryCall(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAllMidsInfoRequest)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`nope`), 422, protocol.Rest)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAllMidsInfoRequest)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"BTC":"1"}`), 200, protocol.Rest)).
		Once()

	detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	assert.Equal(t, map[string]string{"allInfo": "false", "partialInfo": "true"}, detector.DetectLabels())
	assert.Equal(t, map[string]string{"allInfo": "true", "partialInfo": "false"}, detector.DetectLabels())
	connector.AssertExpectations(t)
}
