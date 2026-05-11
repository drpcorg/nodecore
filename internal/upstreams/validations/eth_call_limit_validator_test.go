package validations_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEthCallLimitValidatorReturnsFatalSettingErrorWhenReturnDataLimitIsTooLow(t *testing.T) {
	chain := testEthCallLimitConfiguredChain("0x1234")
	options := &chains.Options{
		InternalTimeout: time.Second,
		CallLimitSize:   1024,
	}
	connector := newEthCallLimitConnectorMock(t, chain, options.CallLimitSize,
		protocol.NewHttpUpstreamResponseWithError(
			protocol.ResponseErrorWithData(1, "rpc.returndata.limit exceeded", nil),
		),
	)

	validator := validations.NewEthCallLimitValidator("upstream-1", connector, chain, options)
	result := validator.Validate()

	assert.Equal(t, validations.FatalSettingError, result)
	connector.AssertExpectations(t)
}

func TestEthCallLimitValidatorReturnsSettingsErrorOnOtherConnectorError(t *testing.T) {
	chain := testEthCallLimitConfiguredChain("0x1234")
	options := &chains.Options{
		InternalTimeout: time.Second,
		CallLimitSize:   1024,
	}
	connector := newEthCallLimitConnectorMock(t, chain, options.CallLimitSize,
		protocol.NewHttpUpstreamResponseWithError(protocol.ServerError()),
	)

	validator := validations.NewEthCallLimitValidator("upstream-1", connector, chain, options)
	result := validator.Validate()

	assert.Equal(t, validations.SettingsError, result)
	connector.AssertExpectations(t)
}

func TestEthCallLimitValidatorReturnsValidOnSuccessfulResponse(t *testing.T) {
	chain := testEthCallLimitConfiguredChain("0xabcd")
	options := &chains.Options{
		InternalTimeout: time.Second,
		CallLimitSize:   1025,
	}
	connector := newEthCallLimitConnectorMock(t, chain, options.CallLimitSize,
		protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"0x1"`), protocol.JsonRpc),
	)

	validator := validations.NewEthCallLimitValidator("upstream-1", connector, chain, options)
	result := validator.Validate()

	assert.Equal(t, validations.Valid, result)
	connector.AssertExpectations(t)
}

func newEthCallLimitConnectorMock(
	t *testing.T,
	chain *chains.ConfiguredChain,
	callLimitSize int64,
	response protocol.ResponseHolder,
) *mocks.ConnectorMock {
	t.Helper()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_call",
		[]any{
			map[string]any{
				"to":   chain.CallValidateContract,
				"data": fmt.Sprintf("0xd8a26e3a%064x", callLimitSize),
			},
			"latest",
		},
		chain.Chain,
	)
	if err != nil {
		t.Fatalf("failed to build eth_call validation request: %v", err)
	}

	connector := mocks.NewConnectorMock()
	call := connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(ethCallLimitRequestMatcher(request))).
		Return(response)

	isIgnored := lo.SomeBy(validations.IgnoredCallLimitErrors, func(item string) bool {
		return response.HasError() && strings.Contains(response.GetError().Message, item)
	})

	if response.HasError() && !isIgnored {
		call.Times(validations.RetryMaxAttempts)
	} else {
		call.Once()
	}

	return connector
}

func ethCallLimitRequestMatcher(expected protocol.RequestHolder) func(protocol.RequestHolder) bool {
	expectedBody, err := expected.Body()
	if err != nil {
		return func(protocol.RequestHolder) bool { return false }
	}

	var expectedPayload any
	if err := sonic.Unmarshal(expectedBody, &expectedPayload); err != nil {
		return func(protocol.RequestHolder) bool { return false }
	}

	return func(got protocol.RequestHolder) bool {
		gotBody, err := got.Body()
		if err != nil {
			return false
		}

		var gotPayload any
		if err := sonic.Unmarshal(gotBody, &gotPayload); err != nil {
			return false
		}

		return reflect.DeepEqual(gotPayload, expectedPayload)
	}
}

func testEthCallLimitConfiguredChain(contract string) *chains.ConfiguredChain {
	return &chains.ConfiguredChain{
		Chain:                chains.ETHEREUM,
		CallValidateContract: contract,
	}
}
