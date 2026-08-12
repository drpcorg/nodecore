package evm_methods_test

import (
	"context"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/methods/evm_methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func evmBase() mapset.Set[string] {
	return mapset.NewThreadUnsafeSet[string](
		"eth_getBalance",
		"eth_getBlockReceipts",
		"trace_block",
		"trace_callMany",
		"debug_storageRangeAt",
	)
}

func TestEvmMethodsDetectorStripsWholeModuleWithoutProbingIt(t *testing.T) {
	connector := mocks.NewConnectorMock()
	// eth only: the trace and debug modules are absent.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"eth":"1.0"}`), protocol.JsonRpc)).
		Once()
	// eth_getBlockReceipts is the only probe-list method whose module survived.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("eth_getBlockReceipts"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewEvmMethodsDetector("upstream-1", chains.ETHEREUM, connector, time.Second, evmBase())

	unsupported := detector.DetectUnsupported(context.Background())

	expected := mapset.NewThreadUnsafeSet[string]("trace_block", "trace_callMany", "debug_storageRangeAt")
	assert.True(t, expected.Equal(unsupported), "expected %v, got %v", expected.ToSlice(), unsupported.ToSlice())
	// The point of staging: a module-level absence is never re-litigated by a probe.
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany")))
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.MatchedBy(requestFor("debug_storageRangeAt")))
}

func TestEvmMethodsDetectorProbesSurvivingModule(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"eth":"1.0","trace":"1.0","debug":"1.0"}`), protocol.JsonRpc)).
		Once()
	// The trace module is on, but this build lacks the specific method.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("the method trace_callMany does not exist/is not available"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("eth_getBlockReceipts"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("debug_storageRangeAt"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{}`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewEvmMethodsDetector("upstream-1", chains.ETHEREUM, connector, time.Second, evmBase())

	unsupported := detector.DetectUnsupported(context.Background())

	expected := mapset.NewThreadUnsafeSet[string]("trace_callMany")
	assert.True(t, expected.Equal(unsupported), "expected %v, got %v", expected.ToSlice(), unsupported.ToSlice())
	assert.False(t, unsupported.ContainsOne("trace_block"), "a present module keeps its non-probed methods")
}

func TestEvmMethodsDetectorUnknownProbeErrorCannotShadowModuleAbsence(t *testing.T) {
	connector := mocks.NewConnectorMock()
	// The trace module is absent, so trace_callMany is stripped in stage 1 and never
	// probed - which is exactly what stops a transient probe failure from saving it.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"eth":"1.0","debug":"1.0"}`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("eth_getBlockReceipts"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("connection reset by peer"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("debug_storageRangeAt"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("connection reset by peer"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := evm_methods.NewEvmMethodsDetector("upstream-1", chains.ETHEREUM, connector, time.Second, evmBase())

	unsupported := detector.DetectUnsupported(context.Background())

	expected := mapset.NewThreadUnsafeSet[string]("trace_block", "trace_callMany")
	assert.True(t, expected.Equal(unsupported), "expected %v, got %v", expected.ToSlice(), unsupported.ToSlice())
	assert.False(t, unsupported.ContainsOne("eth_getBlockReceipts"), "an unknown probe error must not strip a method")
	assert.False(t, unsupported.ContainsOne("debug_storageRangeAt"), "an unknown probe error must not strip a method")
}

func TestEvmMethodsDetectorWithoutRpcModulesStillProbes(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("Method not found"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("Method not found"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("eth_getBlockReceipts"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("debug_storageRangeAt"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{}`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewEvmMethodsDetector("upstream-1", chains.ETHEREUM, connector, time.Second, evmBase())

	unsupported := detector.DetectUnsupported(context.Background())

	expected := mapset.NewThreadUnsafeSet[string]("trace_callMany")
	assert.True(t, expected.Equal(unsupported), "expected %v, got %v", expected.ToSlice(), unsupported.ToSlice())
}

func TestEvmMethodsDetectorSkipsProbesOutsideBase(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"eth":"1.0","trace":"1.0","debug":"1.0"}`), protocol.JsonRpc)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("eth_getBlockReceipts"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc)).
		Once()

	base := mapset.NewThreadUnsafeSet[string]("eth_getBalance", "eth_getBlockReceipts")

	detector := evm_methods.NewEvmMethodsDetector("upstream-1", chains.ETHEREUM, connector, time.Second, base)

	unsupported := detector.DetectUnsupported(context.Background())

	assert.True(t, unsupported.IsEmpty())
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany")))
}
