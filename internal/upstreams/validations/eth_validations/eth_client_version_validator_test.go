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

func TestEthClientVersionValidatorReturnsFatalForBlacklistedVersion(t *testing.T) {
	chain := testClientVersionConfiguredChain(chains.ETHEREUM, []string{"ethereum"})
	connector := newClientVersionConnectorMock(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"erigon/v2.40.0/linux-amd64/go1.20"`), protocol.JsonRpc))

	validator := eth_validations.NewEthClientVersionValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthClientVersionValidatorReturnsValidForAllowedVersion(t *testing.T) {
	chain := testClientVersionConfiguredChain(chains.ETHEREUM, []string{"ethereum"})
	connector := newClientVersionConnectorMock(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"erigon/v2.50.0/linux-amd64/go1.22"`), protocol.JsonRpc))

	validator := eth_validations.NewEthClientVersionValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthClientVersionValidatorIgnoresNetworkSpecificRuleForOtherNetwork(t *testing.T) {
	chain := testClientVersionConfiguredChain(chains.BSC, []string{"bsc"})
	connector := newClientVersionConnectorMock(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"erigon/v2.40.0/linux-amd64/go1.20"`), protocol.JsonRpc))

	validator := eth_validations.NewEthClientVersionValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.Valid, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthClientVersionValidatorMatchesFullRawVersion(t *testing.T) {
	chain := testClientVersionConfiguredChain(chains.BSC, []string{"bsc"})
	connector := newClientVersionConnectorMock(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"reth/v1.6.0-2a4968e/x86_64-unknown-linux-gnu"`), protocol.JsonRpc))

	validator := eth_validations.NewEthClientVersionValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.FatalSettingError, validator.Validate())
	connector.AssertExpectations(t)
}

func TestEthClientVersionValidatorReturnsSettingsErrorOnConnectorError(t *testing.T) {
	chain := testClientVersionConfiguredChain(chains.ETHEREUM, []string{"ethereum"})
	connector := newClientVersionConnectorMock(protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()))

	validator := eth_validations.NewEthClientVersionValidator("upstream-1", connector, chain, &chains.Options{InternalTimeout: time.Second})

	assert.Equal(t, validations.SettingsError, validator.Validate())
	connector.AssertExpectations(t)
}

func newClientVersionConnectorMock(response protocol.ResponseHolder) *mocks.ConnectorMock {
	connector := mocks.NewConnectorMock()
	call := connector.On("SendRequest", mock.Anything, mock.MatchedBy(func(request protocol.RequestHolder) bool {
		return request.Method() == "web3_clientVersion"
	})).Return(response)
	if response.HasError() {
		call.Times(validations.RetryMaxAttempts)
	} else {
		call.Once()
	}
	return connector
}

func testClientVersionConfiguredChain(chain chains.Chain, shortNames []string) *chains.ConfiguredChain {
	return &chains.ConfiguredChain{Chain: chain, ShortNames: shortNames}
}
