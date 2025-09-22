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
	defaultUpState := protocol.DefaultUpstreamState(mocks.NewMethodsMock(), caps, "55")

	expectedState := protocol.UpstreamState{
		Status:          protocol.Unavailable,
		UpstreamMethods: mocks.NewMethodsMock(),
		BlockInfo:       protocol.NewBlockInfo(),
		Caps:            caps,
		HeadData:        &protocol.BlockData{},
		UpstreamIndex:   "55",
	}

	assert.Equal(t, expectedState, defaultUpState)
}

func TestBlockInfo(t *testing.T) {
	blockInfo := protocol.NewBlockInfo()
	blockData := &protocol.BlockData{Height: uint64(75), Hash: "hash"}
	blockData2 := &protocol.BlockData{Height: uint64(86), Hash: "hash1", Slot: uint64(25)}
	assert.NotNil(t, blockInfo)

	blockInfo.AddBlock(blockData, protocol.FinalizedBlock)

	receivedBlockData := blockInfo.GetBlock(protocol.FinalizedBlock)
	assert.Equal(t, blockData, receivedBlockData)

	blockInfo.AddBlock(blockData2, protocol.FinalizedBlock)

	receivedBlockData = blockInfo.GetBlock(protocol.FinalizedBlock)
	assert.Equal(t, blockData2, receivedBlockData)

	blocks := blockInfo.GetBlocks()
	expectedBlocks := map[protocol.BlockType]*protocol.BlockData{
		protocol.FinalizedBlock: blockData2,
	}
	assert.Len(t, blocks, 1)
	assert.Equal(t, expectedBlocks, blocks)
}
