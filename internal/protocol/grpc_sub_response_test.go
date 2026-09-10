package protocol_test

import (
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
)

func TestGrpcSubResponseAccessors(t *testing.T) {
	headers := http.Header{"x-up-meta": {"h"}}
	trailers := map[string][]string{"x-up-trailer": {"t"}}
	frame := &protocol.GrpcSubResponse{Message: []byte("m"), UpstreamId: "up", Headers: headers, Trailers: trailers}

	assert.Equal(t, []byte("m"), frame.GetMessage())
	assert.Nil(t, frame.GetError())
	assert.Equal(t, "up", frame.GetUpstreamId())
	assert.Nil(t, frame.GetParsedEvent())
	assert.Equal(t, headers, frame.ResponseHeaders())
	assert.Equal(t, trailers, frame.ResponseTrailers())
}

func TestGrpcSubResponseErrorFrameCarriesTheStatus(t *testing.T) {
	respErr := protocol.NewGrpcStatusResponseError(&protocol.GrpcStatus{Code: codes.ResourceExhausted, Message: "slow down"})
	frame := &protocol.GrpcSubResponse{Error: respErr, UpstreamId: "up"}

	grpcStatus, ok := protocol.GrpcStatusFromError(frame.GetError())
	assert.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, grpcStatus.Code)
	assert.Equal(t, protocol.GrpcErrorCodeBase+int(codes.ResourceExhausted), frame.GetError().Code)
}

func TestGrpcUpstreamSubscriptionResponse(t *testing.T) {
	messages := make(chan protocol.SubResponse)
	response := protocol.NewGrpcUpstreamSubscriptionResponse(messages, "op-1")

	assert.Equal(t, "op-1", response.OpId())
	assert.Equal(t, messages, response.ResponseChan())
}

func TestSubscriptionEventResponseCarriesMetadata(t *testing.T) {
	headers := http.Header{"x-up-meta": {"h"}}
	trailers := map[string][]string{"x-up-trailer": {"t"}}
	response := protocol.NewSubscriptionEventResponse("1", []byte("r")).
		WithResponseHeaders(headers).
		WithResponseTrailers(trailers)

	assert.Equal(t, headers, response.ResponseHeaders())
	assert.Equal(t, trailers, response.ResponseTrailers())
	assert.Equal(t, []byte("r"), response.ResponseResult())
	assert.False(t, response.IsEnd())
}
