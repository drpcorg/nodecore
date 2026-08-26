package protocol

import (
	"google.golang.org/grpc/codes"
)

// GrpcStatus is the upstream's verbatim gRPC status. It rides through the
// flow as ResponseError.Data so the ingress can hand the client exactly what
// the upstream said, while the classification below drives routing
// independently (the two-track rule).
type GrpcStatus struct {
	Code    codes.Code
	Message string
	// StatusProto is the serialized google.rpc.Status when the upstream
	// attached typed details; nil otherwise. Kept serialized so the protocol
	// layer never parses it.
	StatusProto []byte
}

// GrpcStatusFromError extracts the verbatim upstream status a gRPC error
// response carries, if any.
func GrpcStatusFromError(respError *ResponseError) (*GrpcStatus, bool) {
	if respError == nil {
		return nil, false
	}
	grpcStatus, ok := respError.Data.(*GrpcStatus)
	return grpcStatus, ok
}

// GrpcErrorCodeBase offsets canonical gRPC codes (1-16) out of the
// protocol's own error-code space (the iota block 0-5, the HTTP-ish
// 4xx/5xx and the JSON-RPC -32xxx constants), so no ResponseError.Code
// consumer can ever alias a gRPC code with a nodecore code. The int is
// informational (logs, encoded error bodies); everything that routes or
// renders gRPC errors reads the typed GrpcStatus in Data instead - the
// client-facing status is reconstructed from it, never from this int.
const GrpcErrorCodeBase = 10000

// NewGrpcStatusResponseError builds the ResponseError that carries an upstream
// gRPC status verbatim. The int code is informational; routing and rendering
// read the typed GrpcStatus in Data.
func NewGrpcStatusResponseError(grpcStatus *GrpcStatus) *ResponseError {
	return &ResponseError{
		Code:    GrpcErrorCodeBase + int(grpcStatus.Code),
		Message: grpcStatus.Message,
		Data:    grpcStatus,
	}
}

// NewGrpcStatusError builds a ResponseError carrying a gRPC status as its
// typed GrpcStatus, so the ingress replays it to the client verbatim instead
// of degrading it to INTERNAL (e.g. receive-side errors: client cancel,
// oversized request message).
func NewGrpcStatusError(code codes.Code, message string) *ResponseError {
	return NewGrpcStatusResponseError(&GrpcStatus{Code: code, Message: message})
}

// NewGrpcUpstreamResponse frames a successful unary gRPC reply: the response
// message bytes verbatim, never parsed.
func NewGrpcUpstreamResponse(id string, body []byte) *GenericUpstreamResponse {
	return &GenericUpstreamResponse{
		id:          id,
		result:      body,
		requestType: Grpc,
	}
}

// NewGrpcUpstreamErrorResponse maps a non-OK upstream status onto the
// response shape that routes it correctly. Classification drives routing;
// the original status rides through verbatim inside the ResponseError:
//
//   - client errors (CANCELLED, INVALID_ARGUMENT, NOT_FOUND, ALREADY_EXISTS,
//     FAILED_PRECONDITION, OUT_OF_RANGE) and upstream auth/config problems
//     (PERMISSION_DENIED, UNAUTHENTICATED) pass through with no retry;
//   - transient upstream failures (UNKNOWN, INTERNAL, DATA_LOSS, UNAVAILABLE,
//     ABORTED), timeouts (DEADLINE_EXCEEDED) and throttling
//     (RESOURCE_EXHAUSTED) become partial failures, i.e. retryable on another
//     upstream;
//   - UNIMPLEMENTED is a partial failure too, and additionally bans the
//     method on the upstream via ClassifyMethodAvailability.
func NewGrpcUpstreamErrorResponse(request RequestHolder, grpcStatus *GrpcStatus) ResponseHolder {
	respError := NewGrpcStatusResponseError(grpcStatus)
	switch grpcStatus.Code {
	case codes.Unknown, codes.Internal, codes.DataLoss, codes.Unavailable, codes.Aborted,
		codes.DeadlineExceeded, codes.ResourceExhausted, codes.Unimplemented:
		return NewPartialFailure(request, respError)
	default:
		return &GenericUpstreamResponse{
			id:          request.Id(),
			error:       respError,
			requestType: Grpc,
		}
	}
}

// IsGrpcErrorNotRetryable reports whether an error is a gRPC status whose
// retryability was already decided at construction time by the closed code
// model (retryable codes become partial failures; everything else must NOT be
// re-litigated by the JSON-RPC message matching in errors_config - upstream
// status messages like "transaction not found" would falsely match).
func IsGrpcErrorNotRetryable(respError *ResponseError) bool {
	_, ok := GrpcStatusFromError(respError)
	return ok
}

// IsGrpcRateLimited reports whether a response is an upstream RESOURCE_EXHAUSTED,
// the gRPC analog of HTTP 429 for rate-limit accounting.
func IsGrpcRateLimited(response ResponseHolder) bool {
	if response == nil || !response.HasError() {
		return false
	}
	grpcStatus, ok := GrpcStatusFromError(response.GetError())
	return ok && grpcStatus.Code == codes.ResourceExhausted
}
