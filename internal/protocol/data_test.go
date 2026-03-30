package protocol_test

import (
	"errors"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
		response protocol.ResponseHolder
	}{
		{
			name:     "base response without error",
			expected: false,
			response: protocol.NewSimpleHttpUpstreamResponse("1", []byte("body"), protocol.JsonRpc),
		},
		{
			name:     "base response with a non-retryable error",
			expected: false,
			response: protocol.NewHttpUpstreamResponse("1", []byte(`{"id":"23r23","jsonrpc":"2.0","error":{"message":"super puper err","code":2}}`), 200, protocol.JsonRpc),
		},
		{
			name:     "base response with a retryable error",
			expected: true,
			response: protocol.NewHttpUpstreamResponse("1", []byte(`{"id":"23r23","jsonrpc":"2.0","error":{"message":"missing trie node","code":2}}`), 200, protocol.JsonRpc),
		},
		{
			name:     "reply error with TotalFailure",
			expected: false,
			response: protocol.NewReplyError("1", nil, protocol.JsonRpc, protocol.TotalFailure),
		},
		{
			name:     "reply error with PartialFailure",
			expected: true,
			response: protocol.NewReplyError("1", nil, protocol.JsonRpc, protocol.PartialFailure),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.expected, protocol.IsRetryable(test.response))
		})
	}
}

func TestGetResponseType(t *testing.T) {
	tests := []struct {
		name     string
		wrapper  *protocol.ResponseHolderWrapper
		err      error
		expected protocol.ResultType
	}{
		{
			name:     "StopRetryErr then ResultStop",
			err:      protocol.StopRetryErr{},
			expected: protocol.ResultStop,
		},
		{
			name:     "any error then ResultTotalFailure",
			err:      errors.New("error"),
			expected: protocol.ResultTotalFailure,
		},
		{
			name:     "reply error with PartialFailure then ResultPartialFailure",
			wrapper:  &protocol.ResponseHolderWrapper{Response: protocol.NewReplyError("1", nil, protocol.JsonRpc, protocol.PartialFailure)},
			expected: protocol.ResultPartialFailure,
		},
		{
			name:     "reply error with TotalFailure then ResultTotalFailure",
			wrapper:  &protocol.ResponseHolderWrapper{Response: protocol.NewReplyError("1", nil, protocol.JsonRpc, protocol.TotalFailure)},
			expected: protocol.ResultTotalFailure,
		},
		{
			name:     "response with error then ResultOkWithError",
			wrapper:  &protocol.ResponseHolderWrapper{Response: protocol.NewHttpUpstreamResponse("1", []byte(`{"id":"23r23","jsonrpc":"2.0","error":{"message":"missing trie node","code":2}}`), 200, protocol.JsonRpc)},
			expected: protocol.ResultOkWithError,
		},
		{
			name:     "ok response then ResultOk",
			wrapper:  &protocol.ResponseHolderWrapper{Response: protocol.NewSimpleHttpUpstreamResponse("1", []byte("body"), protocol.JsonRpc)},
			expected: protocol.ResultOk,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.expected, protocol.GetResponseType(test.wrapper, test.err))
		})
	}
}

func TestDefaultUpstreamState(t *testing.T) {
	caps := mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap)
	defaultUpState := protocol.DefaultUpstreamState(mocks.NewMethodsMock(), caps, "55", nil, nil)

	expectedState := protocol.UpstreamState{
		Status:          protocol.Available,
		UpstreamMethods: mocks.NewMethodsMock(),
		BlockInfo:       protocol.NewBlockInfo(),
		LowerBoundsInfo: protocol.NewLowerBoundInfo(),
		Labels:          protocol.NewLabels(),
		Caps:            caps,
		HeadData:        protocol.ZeroBlock{},
		UpstreamIndex:   "55",
	}

	assert.Equal(t, expectedState, defaultUpState)
}

func TestLabelsGetLabel(t *testing.T) {
	labels := protocol.NewLabels()
	labels.AddLabel("region", "us-east-1")

	value, ok := labels.GetLabel("region")

	assert.True(t, ok)
	assert.Equal(t, "us-east-1", value)
}

func TestLabelsGetLabelMissing(t *testing.T) {
	labels := protocol.NewLabels()

	value, ok := labels.GetLabel("missing")

	assert.False(t, ok)
	assert.Empty(t, value)
}

func TestLabelsAddLabelOverwrite(t *testing.T) {
	labels := protocol.NewLabels()
	labels.AddLabel("region", "us-east-1")
	labels.AddLabel("region", "eu-west-1")

	value, ok := labels.GetLabel("region")

	assert.True(t, ok)
	assert.Equal(t, "eu-west-1", value)
}

func TestLabelsGetAllLabels(t *testing.T) {
	labels := protocol.NewLabels()
	labels.AddLabel("region", "us-east-1")
	labels.AddLabel("tier", "archive")

	assert.Equal(t, map[string]string{
		"region": "us-east-1",
		"tier":   "archive",
	}, labels.GetAllLabels())
}

func TestLabelsCopy(t *testing.T) {
	labels := protocol.NewLabels()
	labels.AddLabel("region", "us-east-1")

	copiedLabels := labels.Copy()
	copiedLabels.AddLabel("tier", "archive")
	copiedLabels.AddLabel("region", "eu-west-1")

	originalRegion, ok := labels.GetLabel("region")
	assert.True(t, ok)
	assert.Equal(t, "us-east-1", originalRegion)

	_, originalHasTier := labels.GetLabel("tier")
	assert.False(t, originalHasTier)

	copiedRegion, ok := copiedLabels.GetLabel("region")
	assert.True(t, ok)
	assert.Equal(t, "eu-west-1", copiedRegion)

	copiedTier, ok := copiedLabels.GetLabel("tier")
	assert.True(t, ok)
	assert.Equal(t, "archive", copiedTier)
	assert.NotSame(t, labels, copiedLabels)
}
