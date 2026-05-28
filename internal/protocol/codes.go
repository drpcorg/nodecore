package protocol

import (
	"net/http"
)

func ToHttpCode(response ResponseHolder) int {
	code := http.StatusOK
	switch resp := response.(type) {
	case *ReplyError:
		err := resp.GetError()
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
	case *BaseUpstreamResponse:
		if resp.requestType == Rest {
			return resp.ResponseCode()
		}
	}

	return code
}
