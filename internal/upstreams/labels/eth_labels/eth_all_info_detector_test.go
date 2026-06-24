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

func TestEthAllInfoLabelsDetectorReturnsTrueWhenInfoSucceeds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAllMidsInfoRequest)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`{"BTC":"1"}`), 200, protocol.Rest)).
		Once()

	detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{"allInfo": "true"}, result)
	connector.AssertExpectations(t)
}

func TestEthAllInfoLabelsDetectorReturnsFalseWhenInfoErrors(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(matchAllMidsInfoRequest)).
		Return(protocol.NewHttpUpstreamResponse("1", []byte(`Failed to deserialize the JSON body into the target type`), 422, protocol.Rest)).
		Once()

	detector := eth_labels.NewEthAllInfoLabelsDetector("upstream-id", chains.HYPERLIQUID, time.Second, connector)

	result := detector.DetectLabels()

	assert.Equal(t, map[string]string{"allInfo": "false"}, result)
	connector.AssertExpectations(t)
}
