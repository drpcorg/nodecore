package protocol_test

import (
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func grpcErrorResponse(t *testing.T, code codes.Code) protocol.ResponseHolder {
	t.Helper()
	req := protocol.NewUpstreamGrpcRequest("req-1", "/pkg.Service/Method", nil, nil, "")
	return protocol.NewGrpcUpstreamErrorResponse(req, &protocol.GrpcStatus{Code: code, Message: "upstream said no"})
}

func TestGrpcRetryableStatusesBecomePartialFailures(t *testing.T) {
	for _, code := range []codes.Code{
		codes.Unknown, codes.Internal, codes.DataLoss, codes.Unavailable, codes.Aborted,
		codes.DeadlineExceeded, codes.ResourceExhausted, codes.Unimplemented,
	} {
		response := grpcErrorResponse(t, code)

		replyError, ok := response.(*protocol.ReplyError)
		require.True(t, ok, code.String())
		assert.Equal(t, protocol.PartialFailure, replyError.ErrorKind, code.String())
		assert.True(t, protocol.IsRetryable(response), code.String())
	}
}

func TestGrpcClientAndAuthStatusesPassThroughWithoutRetry(t *testing.T) {
	for _, code := range []codes.Code{
		codes.Canceled, codes.InvalidArgument, codes.NotFound, codes.AlreadyExists,
		codes.FailedPrecondition, codes.OutOfRange,
		codes.PermissionDenied, codes.Unauthenticated,
	} {
		response := grpcErrorResponse(t, code)

		_, isReply := response.(*protocol.ReplyError)
		assert.False(t, isReply, code.String())
		assert.True(t, response.HasError(), code.String())
		assert.False(t, protocol.IsRetryable(response), code.String())
	}
}

func TestGrpcStatusRidesThroughVerbatim(t *testing.T) {
	req := protocol.NewUpstreamGrpcRequest("req-1", "/pkg.Service/Method", nil, nil, "")
	statusProto := []byte{0x08, 0x05}
	response := protocol.NewGrpcUpstreamErrorResponse(req, &protocol.GrpcStatus{
		Code:        codes.NotFound,
		Message:     "object not found",
		StatusProto: statusProto,
	})

	respError := response.GetError()
	require.NotNil(t, respError)
	assert.Equal(t, protocol.GrpcErrorCodeBase+int(codes.NotFound), respError.Code, "gRPC codes are offset out of the protocol error-code space")
	assert.Equal(t, "object not found", respError.Message)

	grpcStatus, ok := protocol.GrpcStatusFromError(respError)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, grpcStatus.Code)
	assert.Equal(t, "object not found", grpcStatus.Message)
	assert.Equal(t, statusProto, grpcStatus.StatusProto)
}

func TestGrpcStatusDrivesMethodAvailability(t *testing.T) {
	classify := func(code codes.Code, message string) protocol.MethodAvailability {
		return protocol.ClassifyMethodAvailability(&protocol.ResponseError{
			Code:    int(code),
			Message: message,
			Data:    &protocol.GrpcStatus{Code: code, Message: message},
		})
	}

	assert.Equal(t, protocol.MethodNotAvailable, classify(codes.Unimplemented, "unknown service"))
	assert.Equal(t, protocol.MethodAvailable, classify(codes.InvalidArgument, "bad object id"))
	assert.Equal(t, protocol.MethodAvailabilityUnknown, classify(codes.Internal, "boom"))
	// JSON-RPC message patterns must not run against gRPC statuses
	assert.Equal(t, protocol.MethodAvailabilityUnknown, classify(codes.Internal, "Method not found"))
}

func TestIsGrpcRateLimited(t *testing.T) {
	assert.True(t, protocol.IsGrpcRateLimited(grpcErrorResponse(t, codes.ResourceExhausted)))
	assert.False(t, protocol.IsGrpcRateLimited(grpcErrorResponse(t, codes.Internal)))
	assert.False(t, protocol.IsGrpcRateLimited(protocol.NewGrpcUpstreamResponse("1", []byte{1})))
	assert.False(t, protocol.IsGrpcRateLimited(nil))
}

func TestNewGrpcUpstreamResponse(t *testing.T) {
	body := []byte{0x0a, 0x01, 0x02}
	response := protocol.NewGrpcUpstreamResponse("req-1", body)

	assert.False(t, response.HasError())
	assert.Equal(t, body, response.ResponseResult())
	encoded, err := io.ReadAll(response.EncodeResponse(nil))
	require.NoError(t, err)
	assert.Equal(t, body, encoded, "gRPC bodies must encode verbatim, with no JSON framing")
}

// gRPC errors made the retryable call at construction; upstream status
// messages must never be re-matched by the JSON-RPC retry patterns
// ("transaction not found" is a retryable pattern for EVM nodes)
func TestGrpcClientErrorMessageIsNotRematchedForRetry(t *testing.T) {
	req := protocol.NewUpstreamGrpcRequest("1", "/pkg.Service/Method", nil, nil, "")
	response := protocol.NewGrpcUpstreamErrorResponse(req, &protocol.GrpcStatus{
		Code:    codes.NotFound,
		Message: "transaction not found",
	})

	assert.False(t, protocol.IsRetryable(response))
}

func TestNewGrpcStatusErrorCarriesTypedStatus(t *testing.T) {
	respError := protocol.NewGrpcStatusError(codes.ResourceExhausted, "grpc: received message larger than max")

	grpcStatus, ok := protocol.GrpcStatusFromError(respError)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, grpcStatus.Code)
	assert.Equal(t, protocol.GrpcErrorCodeBase+int(codes.ResourceExhausted), respError.Code)
}
