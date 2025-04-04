package protocol

import (
	"net/http"
)

func ToHttpCode(response ResponseHolder) int {
	code := http.StatusOK
	if response.HasError() {
		err := response.GetError()
		switch err.Code {
		case ClientErrorCode, WrongChain, NoSupportedMethod:
			code = http.StatusBadRequest
		case RequestTimeout:
			code = http.StatusRequestTimeout
		case InternalServerErrorCode, IncorrectResponseBody:
			code = http.StatusInternalServerError
		default:
			code = http.StatusInternalServerError
		}
	}
	return code
}
