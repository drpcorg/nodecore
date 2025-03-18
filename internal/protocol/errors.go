package protocol

import "fmt"

const (
	BaseError int = iota
	IncorrectResponseBody
	ClientErrorCode         = 400
	InternalServerErrorCode = 500
)

type UpstreamError struct {
	Code    int
	Message string
	Data    interface{}
	cause   error
}

func (b *UpstreamError) Error() string {
	if b.cause != nil {
		return fmt.Sprintf("%d: %s, caused by: %s", b.Code, b.Message, b.cause.Error())
	} else {
		return fmt.Sprintf("%d: %s", b.Code, b.Message)
	}
}

func NewUpstreamErrorFull(code int, message string, data interface{}, cause error) *UpstreamError {
	return &UpstreamError{
		Message: message,
		cause:   cause,
		Code:    code,
		Data:    data,
	}
}

func NewUpstreamErrorWithData(code int, message string, data interface{}) *UpstreamError {
	return &UpstreamError{
		Message: message,
		Code:    code,
		Data:    data,
	}
}

func NewClientUpstreamError(cause error) *UpstreamError {
	return &UpstreamError{
		Message: "client error",
		cause:   cause,
		Code:    ClientErrorCode,
	}
}

func NewServerUpstreamError(cause error) *UpstreamError {
	return &UpstreamError{
		Message: "internal server error",
		cause:   cause,
		Code:    InternalServerErrorCode,
	}
}

func NewIncorrectResponseBodyError(cause error) *UpstreamError {
	return &UpstreamError{
		Message: "incorrect response body",
		cause:   cause,
		Code:    IncorrectResponseBody,
	}
}
