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

// requestFor matches the outgoing internal request for a given method name.
func requestFor(method string) func(request protocol.RequestHolder) bool {
	return func(request protocol.RequestHolder) bool {
		return request.Method() == method
	}
}

func baseSet() mapset.Set[string] {
	return mapset.NewThreadUnsafeSet[string](
		"eth_getBalance",
		"net_version",
		"trace_block",
		"debug_storageRangeAt",
		"unprefixed",
	)
}

func TestRpcModulesDetectorStripsAbsentModules(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"eth":"1.0","net":"1.0","web3":"1.0"}`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewRpcModulesDetector("upstream-1", chains.ETHEREUM, connector, time.Second, baseSet())

	unsupported := detector.DetectUnsupported(context.Background())

	expected := mapset.NewThreadUnsafeSet[string]("trace_block", "debug_storageRangeAt")
	assert.True(t, expected.Equal(unsupported), "expected %v, got %v", expected.ToSlice(), unsupported.ToSlice())
}

func TestRpcModulesDetectorKeepsUnprefixedMethods(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"eth":"1.0"}`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewRpcModulesDetector("upstream-1", chains.ETHEREUM, connector, time.Second, baseSet())

	unsupported := detector.DetectUnsupported(context.Background())

	assert.False(t, unsupported.ContainsOne("unprefixed"), "a method with no module prefix cannot be attributed, so it must be left alone")
}

func TestRpcModulesDetectorErrorMeansNoOpinion(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("method not found"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := evm_methods.NewRpcModulesDetector("upstream-1", chains.ETHEREUM, connector, time.Second, baseSet())

	unsupported := detector.DetectUnsupported(context.Background())

	assert.Nil(t, unsupported, "a node that does not implement rpc_modules contributes nothing and leaves the probes to decide")
}

func TestRpcModulesDetectorMalformedBodyMeansNoOpinion(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`["eth","net"]`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewRpcModulesDetector("upstream-1", chains.ETHEREUM, connector, time.Second, baseSet())

	unsupported := detector.DetectUnsupported(context.Background())

	assert.Nil(t, unsupported, "an unintelligible body is not evidence about modules")
}

func TestRpcModulesDetectorEmptyReplyMeansNoOpinion(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("rpc_modules"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{}`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewRpcModulesDetector("upstream-1", chains.ETHEREUM, connector, time.Second, baseSet())

	unsupported := detector.DetectUnsupported(context.Background())

	assert.Nil(t, unsupported, "an empty module map is indistinguishable from no answer")
}
