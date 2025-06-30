package protocol

import (
	"fmt"
)

const (
	BaseError int = iota
	IncorrectResponseBody
	NoAvailableUpstreams
	WrongChain
	CtxErrorCode
	ClientErrorCode         = 400
	RequestTimeout          = 408
	InternalServerErrorCode = 500
	NoSupportedMethod       = -32601
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

func WrongChainError(chain string) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf("chain %s is not supported", chain),
		Code:    WrongChain,
	}
}
