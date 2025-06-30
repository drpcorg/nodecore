package protocol_test

import (
	"errors"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/stretchr/testify/assert"
	"net/http"
	"testing"
)

func TestCodes(t *testing.T) {
	tests := []struct {
		name         string
		response     protocol.ResponseHolder
		expectedCode int
	}{
		{
			name:         "ok",
			response:     protocol.NewSimpleHttpUpstreamResponse("1", nil, protocol.JsonRpc),
			expectedCode: http.StatusOK,
		},
		{
			name:         "client error",
			response:     protocol.NewTotalFailureFromErr("1", protocol.ClientError(errors.New("err")), protocol.JsonRpc),
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "chain error",
			response:     protocol.NewTotalFailureFromErr("1", protocol.WrongChainError("chain"), protocol.JsonRpc),
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "not supported error",
			response:     protocol.NewTotalFailureFromErr("1", protocol.NotSupportedMethodError("method"), protocol.JsonRpc),
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "request timeout error",
			response:     protocol.NewTotalFailureFromErr("1", protocol.RequestTimeoutError(), protocol.JsonRpc),
			expectedCode: http.StatusRequestTimeout,
		},
		{
			name:         "internal error",
			response:     protocol.NewTotalFailureFromErr("1", protocol.ServerError(), protocol.JsonRpc),
			expectedCode: http.StatusInternalServerError,
		},
		{
			name:         "incorrect response body error",
			response:     protocol.NewTotalFailureFromErr("1", protocol.IncorrectResponseBodyError(errors.New("err")), protocol.JsonRpc),
			expectedCode: http.StatusInternalServerError,
		},
		{
			name:         "default error",
			response:     protocol.NewTotalFailureFromErr("1", &protocol.ResponseError{Code: 1}, protocol.JsonRpc),
			expectedCode: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			code := protocol.ToHttpCode(test.response)

			assert.Equal(te, test.expectedCode, code)
		})
	}
}
