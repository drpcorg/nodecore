package gas_limit_test

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/gas_limit"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMeasure(t *testing.T) {
	result := func(hex string) protocol.ResponseHolder {
		return protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"`+hex+`"`), protocol.JsonRpc)
	}

	tests := []struct {
		name     string
		response protocol.ResponseHolder
		granted  string
	}{
		{name: "honoured request", response: result("0x23c2767c"), granted: "600000000"},
		{name: "32-byte word", response: result("0x" + strings.Repeat("0", 56) + "23c2767c"), granted: "600000000"},
		{name: "upper-case hex", response: result("0x23C2767C"), granted: "600000000"},
		{name: "low cap", response: result("0x2fa20fc"), granted: "50000000"},
		{name: "empty result", response: result("0x")},
		{name: "not hex", response: result("0xzz")},
		{name: "echo of the request", response: result("0x23c34600")},
		{name: "far above the request", response: result("0x" + strings.Repeat("f", 64))},
		{
			name: "json-rpc error",
			response: protocol.NewHttpUpstreamResponseWithError(
				protocol.ResponseErrorWithData(-32000, "gas limit too high", nil),
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			connector.On("SendRequest", mock.Anything, mock.Anything).Return(test.response).Once()

			granted, err := gas_limit.Measure(context.Background(), connector, chains.ETHEREUM, time.Second)

			if test.granted == "" {
				require.Error(t, err)
				assert.Nil(t, granted)
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.granted, granted.String())
			}
			connector.AssertExpectations(t)
		})
	}
}

func TestMeasureSendsCreationCall(t *testing.T) {
	expected, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_call",
		[]any{
			map[string]any{
				"data": "0x5a60005260206000f3",
				"gas":  "0x23C34600",
			},
			"latest",
		},
		chains.ETHEREUM,
	)
	require.NoError(t, err)

	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(sameRequest(expected))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x23c2767c"`), protocol.JsonRpc)).
		Once()

	_, err = gas_limit.Measure(context.Background(), connector, chains.ETHEREUM, time.Second)

	require.NoError(t, err)
	connector.AssertExpectations(t)
}

func TestMeasureWithRetries(t *testing.T) {
	reading := protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x23c2767c"`), protocol.JsonRpc)
	failure := protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())
	measure := func(connector *mocks.ConnectorMock) (*big.Int, error) {
		executor := validations.ValidatorExecutor("upstream-1", "gasLimitTest", nil)
		return gas_limit.MeasureWithRetries(context.Background(), executor, connector, chains.ETHEREUM, 5*time.Second)
	}

	t.Run("a failed call is retried until the node answers", func(t *testing.T) {
		connector := mocks.NewConnectorMock()
		connector.On("SendRequest", mock.Anything, mock.Anything).Return(failure).Twice()
		connector.On("SendRequest", mock.Anything, mock.Anything).Return(reading).Once()

		granted, err := measure(connector)

		require.NoError(t, err)
		assert.Equal(t, "600000000", granted.String())
		connector.AssertExpectations(t)
	})

	t.Run("gives up with the last error when every attempt fails", func(t *testing.T) {
		connector := mocks.NewConnectorMock()
		connector.On("SendRequest", mock.Anything, mock.Anything).Return(failure).Times(validations.RetryMaxAttempts)

		granted, err := measure(connector)

		require.Error(t, err)
		assert.Nil(t, granted)
		connector.AssertExpectations(t)
	})

	t.Run("an answer that is not a reading is not retried", func(t *testing.T) {
		connector := mocks.NewConnectorMock()
		connector.
			On("SendRequest", mock.Anything, mock.Anything).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x"`), protocol.JsonRpc)).
			Once()

		granted, err := measure(connector)

		require.Error(t, err)
		assert.Nil(t, granted)
		connector.AssertExpectations(t)
	})
}

func TestGrantsExtraGas(t *testing.T) {
	assert.False(t, gas_limit.GrantsExtraGas(big.NewInt(50_000_000)))
	assert.False(t, gas_limit.GrantsExtraGas(big.NewInt(590_000_000)))
	assert.True(t, gas_limit.GrantsExtraGas(big.NewInt(590_000_001)))
	assert.True(t, gas_limit.GrantsExtraGas(big.NewInt(600_000_000)))
}

// sameRequest matches a request by method and by JSON-equal body, so an extra
// param (a state override) or a different gas value is a mismatch.
func sameRequest(expected protocol.RequestHolder) func(protocol.RequestHolder) bool {
	return func(actual protocol.RequestHolder) bool {
		if actual == nil || actual.Method() != expected.Method() {
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
		var expectedJSON, actualJSON any
		if sonic.Unmarshal(expectedBody, &expectedJSON) != nil || sonic.Unmarshal(actualBody, &actualJSON) != nil {
			return false
		}
		return assert.ObjectsAreEqual(expectedJSON, actualJSON)
	}
}
