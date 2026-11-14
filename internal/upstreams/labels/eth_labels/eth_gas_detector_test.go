package eth_labels_test

import (
	"context"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
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
	creationCall := func(chain chains.Chain) protocol.RequestHolder {
		request, err := protocol.NewInternalUpstreamJsonRpcRequest(
			"eth_call",
			[]any{
				map[string]any{
					"data": "0x5a60005260206000f3",
					"gas":  "0x23C34600",
				},
				"latest",
			},
			chain,
		)
		require.NoError(t, err)
		return request
	}

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

	result := func(hex string) protocol.ResponseHolder {
		return protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"`+hex+`"`), protocol.JsonRpc)
	}
	rpcError := func(message string) protocol.ResponseHolder {
		return protocol.NewReplyError("1", protocol.ResponseErrorWithData(-32000, message, nil), protocol.JsonRpc, protocol.TotalFailure)
	}
	extraGasLabels := map[string]string{
		"gas-limit":       "600000000",
		"extra_gas_limit": "600000000",
	}

	tests := []struct {
		name          string
		chain         chains.Chain
		basicResponse protocol.ResponseHolder
		monadResponse protocol.ResponseHolder
		expected      map[string]string
	}{
		{
			name:          "extra gas when the node grants the requested budget",
			chain:         chains.ETHEREUM,
			basicResponse: result("0x23c2767c"),
			expected:      extraGasLabels,
		},
		{
			name:          "the granted budget when the node has a lower cap",
			chain:         chains.ETHEREUM,
			basicResponse: result("0x2fa20fc"),
			expected: map[string]string{
				"gas-limit":       "50000000",
				"extra_gas_limit": "50000000",
			},
		},
		{
			name:          "no labels when the node returns an empty result",
			chain:         chains.ETHEREUM,
			basicResponse: result("0x"),
		},
		{
			name:          "no labels when the node echoes the request",
			chain:         chains.ETHEREUM,
			basicResponse: result("0x23c34600"),
		},
		{
			name:          "keeps the basic result on monad when it is already extra gas",
			chain:         chains.MONAD_MAINNET,
			basicResponse: result("0x23c2767c"),
			expected:      extraGasLabels,
		},
		{
			name:          "falls back to the monad probe when the basic result is below the threshold",
			chain:         chains.MONAD_MAINNET,
			basicResponse: result("0x2fa20fc"),
			monadResponse: rpcError("gas limit too high"),
			expected:      extraGasLabels,
		},
		{
			name:          "falls back to the monad probe when the basic call is rejected",
			chain:         chains.MONAD_MAINNET,
			basicResponse: rpcError("user-specified gas exceeds provider limit"),
			monadResponse: rpcError("gas limit too high"),
			expected:      extraGasLabels,
		},
		{
			name:          "no labels on monad when the provider limit is not raised",
			chain:         chains.MONAD_MAINNET,
			basicResponse: rpcError("user-specified gas exceeds provider limit"),
			monadResponse: rpcError("user-specified gas exceeds provider limit"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.
				On("SendRequest", mock.Anything, mock.MatchedBy(gasRequestMatcher(creationCall(test.chain)))).
				Return(test.basicResponse).
				Once()
			if test.monadResponse != nil {
				connector.
					On("SendRequest", mock.Anything, mock.MatchedBy(gasRequestMatcher(monadProbeRequest))).
					Return(test.monadResponse).
					Once()
			}

			detector := eth_labels.NewEthGasLabelsDetector("upstream-id", test.chain, time.Second, connector)

			labels := detector.DetectLabels(context.Background())

			if test.expected == nil {
				assert.Nil(t, labels)
			} else {
				assert.Equal(t, test.expected, labels)
			}
			connector.AssertExpectations(t)
		})
	}
}
