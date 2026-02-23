package protocol_test

import (
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestGetRespKindFromResponse(t *testing.T) {
	tests := []struct {
		name             string
		response         protocol.ResponseHolder
		expectedRespKind protocol.ResponseKind
	}{
		{
			name:             "base response ok",
			response:         protocol.NewSimpleHttpUpstreamResponse("id", nil, protocol.JsonRpc),
			expectedRespKind: protocol.Ok,
		},
		{
			name:             "base response with a non-retryable error",
			expectedRespKind: protocol.Error,
			response:         protocol.NewHttpUpstreamResponse("1", []byte(`{"id":"23r23","jsonrpc":"2.0","error":{"message":"super puper err","code":2}}`), 200, protocol.JsonRpc),
		},
		{
			name:             "base response with a retryable error",
			expectedRespKind: protocol.RetryableError,
			response:         protocol.NewHttpUpstreamResponse("1", []byte(`{"id":"23r23","jsonrpc":"2.0","error":{"message":"missing trie node","code":2}}`), 200, protocol.JsonRpc),
		},
		{
			name:             "reply error with TotalFailure",
			expectedRespKind: protocol.Cancelled,
			response:         protocol.NewReplyError("1", protocol.CtxError(errors.New("err")), protocol.JsonRpc, protocol.TotalFailure),
		},
		{
			name:             "reply error with TotalFailure",
			expectedRespKind: protocol.RoutingError,
			response:         protocol.NewReplyError("1", protocol.NoAvailableUpstreamsError(), protocol.JsonRpc, protocol.TotalFailure),
		},
		{
			name:             "reply error with TotalFailure",
			expectedRespKind: protocol.Error,
			response:         protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.TotalFailure),
		},
		{
			name:             "reply error with PartialFailure",
			expectedRespKind: protocol.RetryableError,
			response:         protocol.NewReplyError("1", protocol.RequestTimeoutError(), protocol.JsonRpc, protocol.PartialFailure),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			respKind := protocol.GetRespKindFromResponse(test.response)
			assert.Equal(te, test.expectedRespKind, respKind)
		})
	}
}

func TestInternalRequests(t *testing.T) {
	tests := []struct {
		name            string
		request         func() protocol.RequestHolder
		expectedReqKind protocol.RequestKind
	}{
		{
			name: "internal unary",
			request: func() protocol.RequestHolder {
				req, _ := protocol.NewInternalUpstreamJsonRpcRequest("method", nil)
				return req
			},
			expectedReqKind: protocol.InternalUnary,
		},
		{
			name: "internal sub",
			request: func() protocol.RequestHolder {
				req, _ := protocol.NewInternalSubUpstreamJsonRpcRequest("method", nil)
				return req
			},
			expectedReqKind: protocol.InternalSubscription,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			actual := test.request().RequestObserver().GetRequestKind()
			assert.Equal(te, test.expectedReqKind, actual)
		})
	}
}

func TestRequestObserverWithUnaryResult(t *testing.T) {
	request := protocol.NewUpstreamJsonRpcRequest("id", nil, "method", nil, false, nil)
	observer := request.RequestObserver().
		WithChain(chains.OPTIMISM).
		WithRequestKind(protocol.Unary).
		WithApiKey("apiKey")

	observer.AddResult(
		protocol.NewUnaryRequestResult().
			WithUpstreamId("upId").
			WithSuccessfulRetry().
			WithDuration(50).
			WithRespKindFromResponse(protocol.NewSimpleHttpUpstreamResponse("id", nil, protocol.JsonRpc)),
		false,
	)
	observer.AddResult(
		protocol.NewUnaryRequestResult().
			WithUpstreamId("upId").
			WithSuccessfulRetry().
			WithDuration(50).
			WithRespKindFromResponse(protocol.NewSimpleHttpUpstreamResponse("id", nil, protocol.JsonRpc)),
		true,
	)

	results := observer.GetResults()
	assert.Len(t, results, 1)

	res, ok := results[0].(*protocol.UnaryRequestResult)
	assert.True(t, ok)

	assert.Equal(t, chains.OPTIMISM, res.GetChain())
	assert.Equal(t, "apiKey", res.GetApiKey())
	assert.Equal(t, "upId", res.GetUpstreamId())
	assert.Equal(t, protocol.Unary, res.GetReqKind())
	assert.Equal(t, "method", res.GetMethod())
	assert.Equal(t, 50.0, res.GetDuration())
	assert.True(t, res.IsSuccessfulRetry())
	assert.Equal(t, protocol.Ok, res.GetRespKind())
	assert.Equal(t, time.UTC, res.GetTimestamp().Location())
}

func TestRequestObserverWithParallelUpstreamUnaryResult(t *testing.T) {
	request := protocol.NewUpstreamJsonRpcRequest("id", nil, "method", nil, false, nil)
	observer := request.RequestObserver().
		WithChain(chains.OPTIMISM).
		WithRequestKind(protocol.Unary).
		WithApiKey("apiKey")

	upstreamCallAndResultFunc := func(upId string, doneDelay time.Duration) {
		done := observer.TrackUpstreamCall()
		go func() {
			defer done()
			time.Sleep(doneDelay)
			observer.AddResult(
				protocol.NewUnaryRequestResult().
					WithUpstreamId(upId).
					WithSuccessfulRetry().
					WithDuration(50).
					WithRespKindFromResponse(protocol.NewSimpleHttpUpstreamResponse("id", nil, protocol.JsonRpc)),
				false,
			)
		}()
	}

	upstreamCallAndResultFunc("upId1", 30*time.Millisecond)
	upstreamCallAndResultFunc("upId2", 10*time.Millisecond)
	upstreamCallAndResultFunc("upId3", 50*time.Millisecond)

	results := observer.GetResults()
	assert.Len(t, results, 3)

	assert.Equal(t, "upId2", results[0].(*protocol.UnaryRequestResult).GetUpstreamId())
	assert.Equal(t, "upId1", results[1].(*protocol.UnaryRequestResult).GetUpstreamId())
	assert.Equal(t, "upId3", results[2].(*protocol.UnaryRequestResult).GetUpstreamId())

	for _, result := range results {
		res, ok := result.(*protocol.UnaryRequestResult)
		assert.True(t, ok)
		assert.Equal(t, chains.OPTIMISM, res.GetChain())
		assert.Equal(t, "apiKey", res.GetApiKey())
		assert.Equal(t, protocol.Unary, res.GetReqKind())
		assert.Equal(t, "method", res.GetMethod())
		assert.Equal(t, 50.0, res.GetDuration())
		assert.True(t, res.IsSuccessfulRetry())
		assert.Equal(t, protocol.Ok, res.GetRespKind())
		assert.Equal(t, time.UTC, res.GetTimestamp().Location())
	}
}
