package labels_test

import (
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func gasRequestMatcher(expected protocol.RequestHolder) func(protocol.RequestHolder) bool {
	return func(actual protocol.RequestHolder) bool {
		if actual == nil || expected == nil {
			return false
		}

		if actual.Id() != expected.Id() ||
			actual.Method() != expected.Method() ||
			actual.RequestType() != expected.RequestType() ||
			actual.IsStream() != expected.IsStream() ||
			actual.IsSubscribe() != expected.IsSubscribe() {
			return false
		}

		expectedBody, err := expected.Body()
		if err != nil {
			return false
		}
		actualBody, err := actual.Body()
		if err != nil {
			return false
		}

		var expectedJSON any
		if err := sonic.Unmarshal(expectedBody, &expectedJSON); err != nil {
			return false
		}
		var actualJSON any
		if err := sonic.Unmarshal(actualBody, &actualJSON); err != nil {
			return false
		}

		return assert.ObjectsAreEqual(expectedJSON, actualJSON)
	}
}

func TestEthGasLabelsDetectorDetectLabels(t *testing.T) {
	basicEthRequest, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_call",
		[]any{
			map[string]any{
				"to":   "0x53Daa71B04d589429f6d3DF52db123913B818F22",
				"data": "0x51be4eaa",
			},
			"latest",
			map[string]any{
				"0x53Daa71B04d589429f6d3DF52db123913B818F22": map[string]any{
					"code": "0x6080604052348015600f57600080fd5b506004361060285760003560e01c806351be4eaa14602d575b600080fd5b60336047565b604051603e91906066565b60405180910390f35b60005a905090565b6000819050919050565b606081604f565b82525050565b6000602082019050607960008301846059565b9291505056fea26469706673582212201c0202887c1afe66974b06ee355dee07542bbc424cf4d1659c91f56c08c3dcc064736f6c63430008130033",
				},
			},
		},
		chains.ETHEREUM,
	)
	require.NoError(t, err)

	basicMonadRequest, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_call",
		[]any{
			map[string]any{
				"to":   "0x53Daa71B04d589429f6d3DF52db123913B818F22",
				"data": "0x51be4eaa",
			},
			"latest",
			map[string]any{
				"0x53Daa71B04d589429f6d3DF52db123913B818F22": map[string]any{
					"code": "0x6080604052348015600f57600080fd5b506004361060285760003560e01c806351be4eaa14602d575b600080fd5b60336047565b604051603e91906066565b60405180910390f35b60005a905090565b6000819050919050565b606081604f565b82525050565b6000602082019050607960008301846059565b9291505056fea26469706673582212201c0202887c1afe66974b06ee355dee07542bbc424cf4d1659c91f56c08c3dcc064736f6c63430008130033",
				},
			},
		},
		chains.MONAD_MAINNET,
	)
	require.NoError(t, err)

	monadProbeRequest, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_call",
		[]any{
			map[string]any{
				"to":   "0x53Daa71B04d589429f6d3DF52db123913B818F22",
				"data": "0x51be4eaa",
				"gas":  "0x232AAF80",
			},
			"latest",
			map[string]any{
				"0x53Daa71B04d589429f6d3DF52db123913B818F22": map[string]any{
					"code": "0x6080604052348015600f57600080fd5b506004361060285760003560e01c806351be4eaa14602d575b600080fd5b60336047565b604051603e91906066565b60405180910390f35b60005a905090565b6000819050919050565b606081604f565b82525050565b6000602082019050607960008301846059565b9291505056fea26469706673582212201c0202887c1afe66974b06ee355dee07542bbc424cf4d1659c91f56c08c3dcc064736f6c63430008130033",
				},
			},
		},
		chains.MONAD_MAINNET,
	)
	require.NoError(t, err)

	t.Run("uses basic gas detection on non-monad chains", func(t *testing.T) {
		connector := mocks.NewConnectorMock()
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(gasRequestMatcher(basicEthRequest))).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x5208"`), protocol.JsonRpc)).
			Once()

		detector := labels.NewEthGasLabelsDetector("upstream-id", chains.ETHEREUM, time.Second, connector)

		result := detector.DetectLabels()

		assert.Equal(t, map[string]string{
			"gas-limit":       "42182",
			"extra_gas_limit": "42182",
		}, result)
		connector.AssertExpectations(t)
	})

	t.Run("keeps basic result on monad when extra gas limit is already capped", func(t *testing.T) {
		connector := mocks.NewConnectorMock()
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(gasRequestMatcher(basicMonadRequest))).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x232a5cc3"`), protocol.JsonRpc)).
			Once()

		detector := labels.NewEthGasLabelsDetector("upstream-id", chains.MONAD_MAINNET, time.Second, connector)

		result := detector.DetectLabels()

		assert.Equal(t, map[string]string{
			"gas-limit":       "590000001",
			"extra_gas_limit": "600000000",
		}, result)
		connector.AssertExpectations(t)
	})

	t.Run("falls back to monad probe when basic result is below threshold", func(t *testing.T) {
		connector := mocks.NewConnectorMock()
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(gasRequestMatcher(basicMonadRequest))).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x5208"`), protocol.JsonRpc)).
			Once()
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(gasRequestMatcher(monadProbeRequest))).
			Return(protocol.NewReplyError("1", protocol.ResponseErrorWithData(-32000, "gas limit too high", nil), protocol.JsonRpc, protocol.TotalFailure)).
			Once()

		detector := labels.NewEthGasLabelsDetector("upstream-id", chains.MONAD_MAINNET, time.Second, connector)

		result := detector.DetectLabels()

		assert.Equal(t, map[string]string{
			"gas-limit":       "600000000",
			"extra_gas_limit": "600000000",
		}, result)
		connector.AssertExpectations(t)
	})
}
