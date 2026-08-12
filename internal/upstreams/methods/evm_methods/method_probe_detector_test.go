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

func TestMethodProbeDetectorStripsAbsentMethod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("the method trace_callMany does not exist/is not available"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("trace_callMany"),
	)

	unsupported := detector.DetectUnsupported(context.Background())

	assert.True(t, mapset.NewThreadUnsafeSet[string]("trace_callMany").Equal(unsupported))
}

func TestMethodProbeDetectorKeepsMethodThatRejectsParams(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("missing value for required argument 0"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("trace_callMany"),
	)

	unsupported := detector.DetectUnsupported(context.Background())

	assert.True(t, unsupported.IsEmpty(), "a params complaint proves the method exists")
}

func TestMethodProbeDetectorKeepsMethodOnUnknownError(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("connection reset by peer"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("trace_callMany"),
	)

	unsupported := detector.DetectUnsupported(context.Background())

	assert.True(t, unsupported.IsEmpty(), "a transient failure must never strip a method")
}

func TestMethodProbeDetectorKeepsMethodOnSuccess(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("eth_getBlockReceipts"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("eth_getBlockReceipts"),
	)

	unsupported := detector.DetectUnsupported(context.Background())

	assert.True(t, unsupported.IsEmpty())
}

func TestMethodProbeDetectorProbesEveryMethod(t *testing.T) {
	connector := mocks.NewConnectorMock()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("Method not found"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("debug_storageRangeAt"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{}`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("trace_callMany", "debug_storageRangeAt"),
	)

	unsupported := detector.DetectUnsupported(context.Background())

	assert.True(t, mapset.NewThreadUnsafeSet[string]("trace_callMany").Equal(unsupported))
	connector.AssertExpectations(t)
}

func TestMethodProbeDetectorNoProbesSendsNothing(t *testing.T) {
	connector := mocks.NewConnectorMock()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string](),
	)

	unsupported := detector.DetectUnsupported(context.Background())

	assert.True(t, unsupported.IsEmpty())
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}
