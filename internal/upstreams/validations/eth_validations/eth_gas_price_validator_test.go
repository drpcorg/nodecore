package eth_validations_test

import (
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

func TestEthGasPriceValidatorReturnsValidWhenConditionPasses(t *testing.T) {
	chain := testGasPriceConfiguredChain([]string{"ne 3000000000"})
	connector := newGasPriceConnectorMock(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xb2d05e001"`), protocol.JsonRpc))

	validator := eth_validations.NewEthGasPriceValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthGasPriceValidatorReturnsFatalWhenConditionFails(t *testing.T) {
	chain := testGasPriceConfiguredChain([]string{"ne 3000000000"})
	connector := newGasPriceConnectorMock(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xb2d05e00"`), protocol.JsonRpc))

	validator := eth_validations.NewEthGasPriceValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthGasPriceValidatorReturnsValidWithoutRules(t *testing.T) {
	chain := testGasPriceConfiguredChain(nil)
	connector := mocks.NewConnectorMock()

	validator := eth_validations.NewEthGasPriceValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestEthGasPriceValidatorReturnsSettingsErrorOnConnectorError(t *testing.T) {
	chain := testGasPriceConfiguredChain([]string{"ne 3000000000"})
	connector := newGasPriceConnectorMock(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	validator := eth_validations.NewEthGasPriceValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.SettingsError, validator.Validate())
	connector.AssertExpectations(t)
}

func newGasPriceConnectorMock(response protocol.ResponseHolder) *mocks.ConnectorMock {
	connector := mocks.NewConnectorMock()
	call := connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(request protocol.RequestHolder) bool {
		return request.Method() == "eth_gasPrice"
	})).Return(response)
	if response.HasError() {
		call.Times(validations.RetryMaxAttempts)
	} else {
		call.Once()
	}
	return connector
}

func testGasPriceConfiguredChain(conditions []string) *chains.ConfiguredChain {
	return &chains.ConfiguredChain{Chain: chains.ETHEREUM, GasPriceCondition: conditions}
}
