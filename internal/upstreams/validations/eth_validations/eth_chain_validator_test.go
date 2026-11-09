package eth_validations_test

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/eth_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestChainValidatorChaiIdErrorThenSettingErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewTotalFailure(chainIdRequest, protocol.RequestTimeoutError()),
	)
	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"1"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.UnknownChain, options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.SettingsError, actualResult)
}

func TestChainValidatorNetVersionErrorThenSettingErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewTotalFailure(netVersionRequest, protocol.RequestTimeoutError()),
	)
	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.UnknownChain, options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.SettingsError, actualResult)
}

func TestChainValidatorWrongChainSettingsThenFatalErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
	)
	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.GetChain("ethereum"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.FatalSettingError, actualResult)
}

func TestChainValidatorValidResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
	)
	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"56"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.GetChain("bsc"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.Valid, actualResult)
}

func TestChainValidatorHexNetVersionConvertedThenValid(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
	)
	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0X38"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.GetChain("bsc"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.Valid, actualResult)
}

func TestChainValidatorLargeHexNetVersionConvertedThenFatalErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
	)
	// 0xffffffffffffffffff overflows uint64 — proves big.Int path works (won't match BSC's "56" so result is FatalSettingError, but conversion itself must not error).
	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xffffffffffffffffff"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.GetChain("bsc"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.FatalSettingError, actualResult)
}

func TestChainValidatorInvalidHexNetVersionThenSettingErrorResult(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.ETHEREUM)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.ETHEREUM)

	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x38"`), protocol.JsonRpc),
	)
	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0xzz"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.GetChain("bsc"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.SettingsError, actualResult)
}

// A cosmos chain with an EVM module is validated against its EVM ids from
// chain-ids, not against the cosmos network name in chain-id. Injective
// testnet also answers a net_version that is not the decimal of eth_chainId.
func TestChainValidatorCosmosEvmChainUsesTheEvmIds(t *testing.T) {
	connector := mocks.NewConnectorMock()
	options := &chains.Options{
		InternalTimeout: time.Second,
	}
	chainIdRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("eth_chainId", nil, chains.INJECTIVE_TESTNET)
	netVersionRequest, _ := protocol.NewInternalUpstreamJsonRpcRequest("net_version", nil, chains.INJECTIVE_TESTNET)

	test_utils.ExpectEthValidationRequest(
		connector,
		chainIdRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x59f"`), protocol.JsonRpc),
	)
	test_utils.ExpectEthValidationRequest(
		connector,
		netVersionRequest,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"888"`), protocol.JsonRpc),
	)

	validator := eth_validations.NewEthChainValidator("id", connector, chains.GetChain("injective-testnet"), options)
	actualResult := validator.Validate()

	connector.AssertExpectations(t)
	assert.Equal(t, validations.Valid, actualResult)
}
