package protocol

import (
	"fmt"
)

const (
	BaseError int = iota
	NoAvailableUpstreams
	WrongChain
	CtxErrorCode
	WsTotalFailure
	ClientErrorCode         = 400
	AuthErrorCode           = 403
	RequestTimeout          = 408
	InternalServerErrorCode = 500
	RateLimitExceeded       = 429
	NoSupportedMethod       = -32601
	IncorrectResponseBody   = -32001
	QuorumSignatureErrCode  = -32010
)

type ResponseError struct {
	Code    int
	Message string
	Data    interface{}
}

func (b *ResponseError) Error() string {
	return fmt.Sprintf("%d: %s", b.Code, b.Message)
}

func ResponseErrorWithMessage(message string) *ResponseError {
	return &ResponseError{
		Message: message,
	}
}

func ResponseErrorWithData(code int, message string, data interface{}) *ResponseError {
	return &ResponseError{
		Message: message,
		Code:    code,
		Data:    data,
	}
}

func AuthError(cause error) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("auth error - %s", cause.Error()),
		Code:    AuthErrorCode,
	}
}

func ClientError(cause error) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("client error - %s", cause.Error()),
		Code:    ClientErrorCode,
	}
}

func ParseError() *ResponseError {
	return &ResponseError{
		Message: "couldn't parse a request",
		Code:    ClientErrorCode,
	}
}

func RequestTimeoutError() *ResponseError {
	return &ResponseError{
		Message: "request timeout",
		Code:    RequestTimeout,
	}
}

func ServerErrorWithCause(cause error) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("internal server error: %s", cause.Error()),
		Code:    InternalServerErrorCode,
	}
}

func CtxError(cause error) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("ctx error: %s", cause.Error()),
		Code:    CtxErrorCode,
	}
}

func ServerError() *ResponseError {
	return &ResponseError{
		Message: "internal server error",
		Code:    InternalServerErrorCode,
	}
}

func IncorrectResponseBodyError(cause error) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("incorrect response body: %s", cause.Error()),
		Code:    IncorrectResponseBody,
	}
}

func NoAvailableUpstreamsError() *ResponseError {
	return &ResponseError{
		Message: "no available upstreams to process a request",
		Code:    NoAvailableUpstreams,
	}
}

func NotSupportedMethodError(method string) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("the method %s does not exist/is not available", method),
		Code:    NoSupportedMethod,
	}
}

func RateLimitError() *ResponseError {
	return &ResponseError{
		Message: "rate limit exceeded",
		Code:    RateLimitExceeded,
	}
}

func WrongChainError(chain string) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("chain %s is not supported", chain),
		Code:    WrongChain,
	}
}

func WsTotalFailureError() *ResponseError {
	return &ResponseError{
		Message: "websocket total failure",
		Code:    WsTotalFailure,
	}
}

func QuorumUnknownProviderError(providerID string) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("quorum signature verification failed: no public key configured for provider %q", providerID),
		Code:    QuorumSignatureErrCode,
	}
}

func QuorumInvalidSignatureError(providerID string) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("quorum signature verification failed: invalid signature from provider %q", providerID),
		Code:    QuorumSignatureErrCode,
	}
}

func QuorumMissingSignaturesError() *ResponseError {
	return &ResponseError{
		Message: "quorum signature verification failed: no signatures returned by upstream",
		Code:    QuorumSignatureErrCode,
	}
}

func QuorumInsufficientSignaturesError(got, required int) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("quorum signature verification failed: got %d signatures, required %d", got, required),
		Code:    QuorumSignatureErrCode,
	}
}

func QuorumMalformedHeaderError(cause error) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("quorum signature verification failed: malformed QR header - %s", cause.Error()),
		Code:    QuorumSignatureErrCode,
	}
}

func QuorumUnexpectedRequestIDError(expected, got string) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("quorum signature verification failed: QR header request id mismatch (expected %q, got %q)", expected, got),
		Code:    QuorumSignatureErrCode,
	}
}

func QuorumVerificationError(cause error) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("quorum signature verification failed: %s", cause.Error()),
		Code:    QuorumSignatureErrCode,
	}
}

func QuorumNotSupportedError(reason string) *ResponseError {
	msg := "quorum is not supported for this request"
	if reason != "" {
		msg = fmt.Sprintf("%s: %s", msg, reason)
	}
	return &ResponseError{
		Message: msg,
		Code:    QuorumSignatureErrCode,
	}
}
