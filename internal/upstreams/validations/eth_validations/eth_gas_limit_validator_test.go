package eth_validations_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/eth_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEthGasLimitValidator(t *testing.T) {
	result := func(hex string) protocol.ResponseHolder {
		return protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"`+hex+`"`), protocol.JsonRpc)
	}

	tests := []struct {
		name     string
		response protocol.ResponseHolder
		attempts int
		expected validations.ValidationSettingResult
	}{
		{name: "valid when the node grants the requested budget", response: result("0x23c2767c"), expected: validations.Valid},
		{name: "valid just above the threshold", response: result("0x2329dffd"), expected: validations.Valid},
		{name: "fatal at the threshold", response: result("0x2329dffc"), expected: validations.FatalSettingError},
		{name: "fatal when the node has a low cap", response: result("0x2fa20fc"), expected: validations.FatalSettingError},
		{name: "settings error on an empty result", response: result("0x"), expected: validations.SettingsError},
		{name: "settings error when the node echoes the request", response: result("0x23c34600"), expected: validations.SettingsError},
		{
			name: "settings error on a json-rpc error",
			response: protocol.NewHttpUpstreamResponseWithError(
				protocol.ResponseErrorWithData(-32000, "gas limit too high", nil),
			),
			attempts: validations.RetryMaxAttempts,
			expected: validations.SettingsError,
		},
		{
			name:     "settings error on a transport failure",
			response: protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()),
			attempts: validations.RetryMaxAttempts,
			expected: validations.SettingsError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector := mocks.NewConnectorMock()
			// an error response is retried, any other answer is final after one call
			connector.On("SendRequest", mock.Anything, mock.Anything).Return(test.response).Times(max(test.attempts, 1))

			validator := eth_validations.NewEthGasLimitValidator(
				"upstream-1",
				connector,
				chains.GetChain("ethereum"),
				&chains.Options{InternalTimeout: 5 * time.Second},
			)

			assert.Equal(t, test.expected, validator.Validate(context.Background()))
			connector.AssertExpectations(t)
		})
	}
}

func TestEthGasLimitValidatorRetriesAFailedCall(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x23c2767c"`), protocol.JsonRpc)).
		Once()

	validator := eth_validations.NewEthGasLimitValidator(
		"upstream-1",
		connector,
		chains.GetChain("ethereum"),
		&chains.Options{InternalTimeout: 5 * time.Second},
	)

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	connector.AssertExpectations(t)
}
