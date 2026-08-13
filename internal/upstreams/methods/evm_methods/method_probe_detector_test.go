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
	"github.com/stretchr/testify/require"
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

func TestMethodProbeDetectorNilWhenNothingWasEverLearned(t *testing.T) {
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

	assert.Nil(t, unsupported, "a transient failure must neither strip a method nor claim everything is supported")
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

	assert.Nil(t, unsupported, "with nothing to ask there is nothing to contribute")
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestMethodProbeDetectorRetainsAnswersPerProbe(t *testing.T) {
	connector := mocks.NewConnectorMock()
	// Round 1: both answer. Round 2: trace_callMany times out while the other still
	// answers. Losing the retained answer would restore trace_callMany.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("Method not found"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("connection reset by peer"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("debug_storageRangeAt"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{}`), protocol.JsonRpc)).
		Twice()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("trace_callMany", "debug_storageRangeAt"),
	)

	first := detector.DetectUnsupported(context.Background())
	require.True(t, mapset.NewThreadUnsafeSet[string]("trace_callMany").Equal(first), "got %v", first.ToSlice())

	second := detector.DetectUnsupported(context.Background())
	assert.True(t, mapset.NewThreadUnsafeSet[string]("trace_callMany").Equal(second),
		"a probe that could not be reached must keep its last answer; got %v", second.ToSlice())
}

func TestMethodProbeDetectorConclusiveAnswerReplacesTheRetainedOne(t *testing.T) {
	connector := mocks.NewConnectorMock()
	// The node gains the method between rounds, so the retained "absent" must be dropped.
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewReplyError("1", protocol.ResponseErrorWithMessage("Method not found"), protocol.JsonRpc, protocol.TotalFailure)).
		Once()
	connector.
		On("SendRequest", mock.Anything, mock.MatchedBy(requestFor("trace_callMany"))).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[]`), protocol.JsonRpc)).
		Once()

	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("trace_callMany"),
	)

	require.False(t, detector.DetectUnsupported(context.Background()).IsEmpty())
	assert.True(t, detector.DetectUnsupported(context.Background()).IsEmpty(), "a definite answer must replace the retained one")
}

func TestMethodProbeDetectorIgnoresMethodsOutsideTheSpec(t *testing.T) {
	connector := mocks.NewConnectorMock()

	// eth_getBalance is not on the probe list, and trace_callMany is not in this base, so
	// nothing should be asked at all.
	detector := evm_methods.NewMethodProbeDetector(
		"upstream-1", chains.ETHEREUM, connector, time.Second,
		mapset.NewThreadUnsafeSet[string]("eth_getBalance"),
	)

	assert.Nil(t, detector.DetectUnsupported(context.Background()))
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}
