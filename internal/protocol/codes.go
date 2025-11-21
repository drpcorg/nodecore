package protocol

import (
	"net/http"
)

func ToHttpCode(response ResponseHolder) int {
	replyErr, ok := response.(*ReplyError)
	code := http.StatusOK
	if ok {
		err := replyErr.GetError()
		switch err.Code {
		case ClientErrorCode, WrongChain, NoSupportedMethod:
			code = http.StatusBadRequest
		case AuthErrorCode:
			code = http.StatusForbidden
		case RequestTimeout:
			code = http.StatusRequestTimeout
		case InternalServerErrorCode, IncorrectResponseBody:
			code = http.StatusInternalServerError
		case RateLimitExceeded:
			code = http.StatusTooManyRequests
		default:
			code = http.StatusInternalServerError
		}
	}

	return code
}
