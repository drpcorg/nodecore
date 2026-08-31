package protocol

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestGrpcStatusOfReplaysUpstreamStatusWithDetails(t *testing.T) {
	upstream, err := status.New(codes.NotFound, "object not found").
		WithDetails(&errdetails.ErrorInfo{Reason: "OBJECT_PRUNED", Domain: "sui.io"})
	require.NoError(t, err)
	statusProto, err := proto.Marshal(upstream.Proto())
	require.NoError(t, err)

	got, fromStatusProto := GrpcStatusOf(NewGrpcStatusResponseError(&GrpcStatus{Code: codes.NotFound, Message: "object not found", StatusProto: statusProto}))

	assert.True(t, fromStatusProto)
	assert.Equal(t, codes.NotFound, got.Code())
	assert.Equal(t, "object not found", got.Message())
	require.Len(t, got.Details(), 1)
	assert.Equal(t, "OBJECT_PRUNED", got.Details()[0].(*errdetails.ErrorInfo).Reason)
}

func TestGrpcStatusOfReplaysUpstreamStatusWithoutDetails(t *testing.T) {
	got, fromStatusProto := GrpcStatusOf(NewGrpcStatusError(codes.ResourceExhausted, "slow down"))
	assert.False(t, fromStatusProto)
	assert.Equal(t, codes.ResourceExhausted, got.Code())
	assert.Equal(t, "slow down", got.Message())
	assert.Empty(t, got.Details())
}

func TestGrpcStatusOfMapsNodecoreErrors(t *testing.T) {
	cases := map[string]struct {
		err  *ResponseError
		code codes.Code
	}{
		"no upstreams":       {NoAvailableUpstreamsError(), codes.Unavailable},
		"wrong chain":        {WrongChainError("x"), codes.InvalidArgument},
		"client":             {ClientError(assert.AnError), codes.InvalidArgument},
		"not supported":      {NotSupportedMethodError("m"), codes.Unimplemented},
		"rate limit":         {&ResponseError{Code: RateLimitExceeded, Message: "rl"}, codes.ResourceExhausted},
		"timeout":            {&ResponseError{Code: RequestTimeout, Message: "t"}, codes.DeadlineExceeded},
		"auth":               {&ResponseError{Code: AuthErrorCode, Message: "a"}, codes.PermissionDenied},
		"internal (default)": {ServerError(), codes.Internal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, fromStatusProto := GrpcStatusOf(tc.err)
			assert.False(t, fromStatusProto)
			assert.Equal(t, tc.code, got.Code())
			assert.Equal(t, tc.err.Message, got.Message())
		})
	}
}

func TestGrpcStatusOfNilIsInternal(t *testing.T) {
	got, fromStatusProto := GrpcStatusOf(nil)
	assert.False(t, fromStatusProto)
	assert.Equal(t, codes.Internal, got.Code())
}

func TestGrpcStatusOfCorruptStatusProtoFallsBackToCodeAndMessage(t *testing.T) {
	got, fromStatusProto := GrpcStatusOf(NewGrpcStatusResponseError(
		&GrpcStatus{Code: codes.NotFound, Message: "object not found", StatusProto: []byte{0xff}}))

	assert.False(t, fromStatusProto)
	assert.Equal(t, codes.NotFound, got.Code())
	assert.Equal(t, "object not found", got.Message())
}
