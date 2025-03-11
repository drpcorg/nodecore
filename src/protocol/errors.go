package protocol

import "fmt"

const (
	IncorrectResponseBody   int = iota
	ClientErrorCode             = 400
	InternalServerErrorCode     = 500
)

type UpstreamError struct {
	code    int
	message string
	data    interface{}
	cause   error
}

func (b *UpstreamError) Error() string {
	if b.cause != nil {
		return fmt.Sprintf("%s - caused by: %s", b.message, b.cause.Error())
	} else {
		return b.message
	}
}

func NewUpstreamErrorFull(code int, message string, data interface{}, cause error) *UpstreamError {
	return &UpstreamError{
		message: message,
		cause:   cause,
		code:    code,
		data:    data,
	}
}

func NewUpstreamErrorWithData(code int, message string, data interface{}) *UpstreamError {
	return &UpstreamError{
		message: message,
		code:    code,
		data:    data,
	}
}

func NewClientUpstreamError(cause error) *UpstreamError {
	return &UpstreamError{
		message: "client error",
		cause:   cause,
		code:    ClientErrorCode,
	}
}

func NewServerUpstreamError(cause error) *UpstreamError {
	return &UpstreamError{
		message: "internal server error",
		cause:   cause,
		code:    InternalServerErrorCode,
	}
}

func NewIncorrectResponseBodyError(cause error) *UpstreamError {
	return &UpstreamError{
		message: "incorrect response body",
		cause:   cause,
		code:    IncorrectResponseBody,
	}
}
